package generator

import (
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

func generateWSHandler(svc *parser.Service, m *parser.Method) string {
	data := wsData{
		ServiceName:     svc.Name,
		MethodName:      m.Name,
		HandlerFuncName: toLowerCamel(m.Name) + "WSHandler",
		FactoryName:     toLowerCamel(m.Name) + "StreamFactory",
		ProxyName:       toLowerCamel(m.Name) + "StreamProxy",
		InputTypeName:   m.InputType.Name,
		ExcludeAuth:     m.ExcludeAuth,
	}
	// wsHandlerTmpl emits a Go fragment (no package/imports) that the parent
	// template inlines, so we skip format.Source here — it would reject the
	// fragment as invalid syntax. The enclosing service file gets gofmt'd
	// in renderTemplate after assembly.
	return renderFragment(wsHandlerTmpl, data)
}
