// Example gRPC backend server with in-memory storage.
// This is the backend that protobridge-generated proxy would connect to.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/mrs1lentcz/gox/grpcx/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/mrs1lentcz/protobridge/example/taskboard/gen/grpc/taskboard/v1"
)

func main() {
	svc := &taskService{
		tasks:    make(map[string]*pb.Task),
		watchers: make(map[string]chan *pb.TaskEvent),
	}
	authSvc := &authService{}

	log.Println("starting gRPC server on :50051")
	if err := server.Run(context.Background(), server.Config{
		Addr:       ":50051",
		Reflection: true,
		RegisterServices: func(s *grpc.Server) error {
			pb.RegisterTaskServiceServer(s, svc)
			pb.RegisterAuthServiceServer(s, authSvc)
			return nil
		},
	}); err != nil {
		log.Fatal(err)
	}
}

// =============================================================================
// Auth Service
// =============================================================================

type authService struct {
	pb.UnimplementedAuthServiceServer
}

func (s *authService) Authenticate(ctx context.Context, req *pb.AuthRequest) (*pb.AuthResponse, error) {
	token := req.Headers["Authorization"]
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing Authorization header")
	}

	// Dummy auth: token value is treated as user_id.
	return &pb.AuthResponse{
		UserId:   token,
		Username: "user-" + token,
		Role:     "admin",
	}, nil
}

// =============================================================================
// Task Service
// =============================================================================

type taskService struct {
	pb.UnimplementedTaskServiceServer
	mu       sync.RWMutex
	tasks    map[string]*pb.Task
	watchers map[string]chan *pb.TaskEvent
}

func (s *taskService) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tasks)
}

func (s *taskService) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	user := authUser(ctx)

	task := &pb.Task{
		Id:          uuid.New().String(),
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Status:      pb.TaskStatus_TASK_STATUS_TODO,
		AssigneeId:  req.AssigneeId,
		Tags:        req.Tags,
		CreatedBy:   user.GetUserId(),
		UpdatedBy:   user.GetUserId(),
	}

	s.mu.Lock()
	s.tasks[task.Id] = task
	s.mu.Unlock()

	s.broadcast(&pb.TaskEvent{EventType: "created", Task: task})
	return task, nil
}

func (s *taskService) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.Task, error) {
	taskID := mdValue(ctx, "task_id")
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	s.mu.RLock()
	task, ok := s.tasks[taskID]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("task %s not found", taskID))
	}
	return task, nil
}

func (s *taskService) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	user := authUser(ctx)
	taskID := mdValue(ctx, "task_id")
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, fmt.Sprintf("task %s not found", taskID))
	}

	if req.Title != "" {
		task.Title = req.Title
	}
	if req.Description != "" {
		task.Description = req.Description
	}
	if req.Priority != pb.TaskPriority_TASK_PRIORITY_UNSPECIFIED {
		task.Priority = req.Priority
	}
	if req.Status != pb.TaskStatus_TASK_STATUS_UNSPECIFIED {
		task.Status = req.Status
	}
	if req.AssigneeId != "" {
		task.AssigneeId = req.AssigneeId
	}
	if len(req.Tags) > 0 {
		task.Tags = req.Tags
	}
	task.UpdatedBy = user.GetUserId()
	s.mu.Unlock()

	s.broadcast(&pb.TaskEvent{EventType: "updated", Task: task})
	return task, nil
}

func (s *taskService) DeleteTask(ctx context.Context, req *pb.DeleteTaskRequest) (*pb.DeleteTaskResponse, error) {
	user := authUser(ctx)
	taskID := mdValue(ctx, "task_id")
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if ok {
		delete(s.tasks, taskID)
	}
	s.mu.Unlock()

	if !ok {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("task %s not found", taskID))
	}

	s.broadcast(&pb.TaskEvent{EventType: "deleted", Task: task})
	return &pb.DeleteTaskResponse{
		Success:   true,
		DeletedBy: user.GetUserId(),
	}, nil
}

func (s *taskService) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var filtered []*pb.Task
	for _, t := range s.tasks {
		if req.StatusFilter != pb.TaskStatus_TASK_STATUS_UNSPECIFIED && t.Status != req.StatusFilter {
			continue
		}
		if req.PriorityFilter != pb.TaskPriority_TASK_PRIORITY_UNSPECIFIED && t.Priority != req.PriorityFilter {
			continue
		}
		filtered = append(filtered, t)
	}

	page, limit := int32(1), int32(20)
	if req.Paging != nil {
		if req.Paging.Page > 0 {
			page = req.Paging.Page
		}
		if req.Paging.Limit > 0 {
			limit = req.Paging.Limit
		}
	}
	start := (page - 1) * limit
	end := start + limit
	total := int32(len(filtered))
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	return &pb.ListTasksResponse{
		Tasks: filtered[start:end],
		Total: total,
	}, nil
}

// Server streaming – push task events.
func (s *taskService) WatchTasks(req *pb.WatchTasksRequest, stream pb.TaskService_WatchTasksServer) error {
	id := uuid.New().String()
	ch := make(chan *pb.TaskEvent, 32)

	s.mu.Lock()
	s.watchers[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.watchers, id)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case event := <-ch:
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

// Server streaming via SSE – task notifications (same logic as WatchTasks).
func (s *taskService) TaskNotifications(req *pb.WatchTasksRequest, stream pb.TaskService_TaskNotificationsServer) error {
	return s.WatchTasks(req, stream)
}

// Server streaming – public activity feed (broadcast, no per-user routing).
func (s *taskService) ActivityFeed(req *pb.WatchTasksRequest, stream pb.TaskService_ActivityFeedServer) error {
	return s.WatchTasks(req, stream)
}

// Client streaming – bulk create tasks.
func (s *taskService) BulkCreateTasks(stream pb.TaskService_BulkCreateTasksServer) error {
	user := authUser(stream.Context())
	var ids []string
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.BulkCreateTaskResponse{
				CreatedCount: int32(len(ids)),
				Ids:          ids,
			})
		}
		if err != nil {
			return err
		}

		task := &pb.Task{
			Id:          uuid.New().String(),
			Title:       req.Title,
			Description: req.Description,
			Priority:    req.Priority,
			Status:      pb.TaskStatus_TASK_STATUS_TODO,
			CreatedBy:   user.GetUserId(),
			UpdatedBy:   user.GetUserId(),
		}

		s.mu.Lock()
		s.tasks[task.Id] = task
		s.mu.Unlock()

		ids = append(ids, task.Id)
		s.broadcast(&pb.TaskEvent{EventType: "created", Task: task})
	}
}

// Bidi streaming – simple echo chat per task.
func (s *taskService) TaskChat(stream pb.TaskService_TaskChatServer) error {
	user := authUser(stream.Context())
	botName := "bot"
	if user != nil {
		botName = fmt.Sprintf("bot (replying to %s)", user.GetUsername())
	}

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		reply := &pb.ChatMessage{
			Content: fmt.Sprintf("[%s]: %s", botName, msg.Content),
			Sender:  botName,
		}
		if err := stream.Send(reply); err != nil {
			return err
		}
	}
}

func (s *taskService) broadcast(event *pb.TaskEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

func mdValue(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// authUser extracts the AuthResponse from x-protobridge-user metadata.
// The proxy serializes the auth RPC response via proto.Marshal → base64
// and forwards it as this metadata key on every downstream call.
func authUser(ctx context.Context) *pb.AuthResponse {
	encoded := mdValue(ctx, "x-protobridge-user")
	if encoded == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	user := &pb.AuthResponse{}
	if err := proto.Unmarshal(data, user); err != nil {
		return nil
	}
	return user
}
