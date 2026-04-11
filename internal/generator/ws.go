package generator

import (
	"bytes"
	"text/template"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

var wsHandlerTmpl = template.Must(template.New("ws").Parse(`
func {{ .HandlerFuncName }}(conn *grpc.ClientConn, auth runtime.AuthFunc) http.HandlerFunc {
	factory := &{{ .FactoryName }}{}
	return runtime.WSHandler(conn, factory, auth, {{ .ExcludeAuth }})
}

type {{ .FactoryName }} struct{}

func (f *{{ .FactoryName }}) OpenStream(ctx context.Context, conn *grpc.ClientConn) (runtime.StreamProxy, error) {
	client := pb.New{{ .ServiceName }}Client(conn)
	stream, err := client.{{ .MethodName }}(ctx)
	if err != nil {
		return nil, err
	}
	return &{{ .ProxyName }}{stream: stream}, nil
}

type {{ .ProxyName }} struct {
	stream pb.{{ .ServiceName }}_{{ .MethodName }}Client
}

func (p *{{ .ProxyName }}) Send(msg proto.Message) error {
	return p.stream.Send(msg.(*pb.{{ .InputTypeName }}))
}

func (p *{{ .ProxyName }}) Recv() (proto.Message, error) {
	return p.stream.Recv()
}

func (p *{{ .ProxyName }}) NewRequestMessage() proto.Message {
	return &pb.{{ .InputTypeName }}{}
}

func (p *{{ .ProxyName }}) CloseSend() error {
	return p.stream.CloseSend()
}
`))

type wsData struct {
	ServiceName     string
	MethodName      string
	HandlerFuncName string
	FactoryName     string
	ProxyName       string
	InputTypeName   string
	ExcludeAuth     bool
}

func generateWSHandler(svc *parser.Service, m *parser.Method) (string, error) {
	data := wsData{
		ServiceName:     svc.Name,
		MethodName:      m.Name,
		HandlerFuncName: toLowerCamel(m.Name) + "WSHandler",
		FactoryName:     toLowerCamel(m.Name) + "StreamFactory",
		ProxyName:       toLowerCamel(m.Name) + "StreamProxy",
		InputTypeName:   m.InputType.Name,
		ExcludeAuth:     m.ExcludeAuth,
	}

	var buf bytes.Buffer
	if err := wsHandlerTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
