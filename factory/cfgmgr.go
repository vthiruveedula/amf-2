// +build cfgmgr

package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/coreswitch/cmd"
	rpc "github.com/coreswitch/openconfigd/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

const (
	mgmtServerURL         = ":2650"
	mgmtConnRetryInterval = 5
)

var (
	lis net.Listener
	ch  chan interface{}
)

// Register amf with cli port 2701
func registerModule(client rpc.RegisterClient) error {
	req := &rpc.RegisterModuleRequest{
		Module: "amf",
		Port:   "2701",
	}
	_, err := client.DoRegisterModule(context.Background(), req)
	if err != nil {
		return err
	}
	return nil
}

// cli definition by JSON format.
const cliSpec = `
[
    {
        "name": "show_amf_session",
        "line": "show amf session",
        "mode": "exec",
        "helps": [
            "Show running system information",
            "AMF information",
            "AMF sessions"
        ]
    },
    {
        "name": "show_amf_status",
        "line": "show amf status",
        "mode": "exec",
        "helps": [
            "Show running system information",
            "AMF information",
            "AMF status"
        ]
    }
]
`

var cliMap = map[string]func(*cliTask, []interface{}){
	"show_amf_status":  showAMFStatus,
	"show_amf_session": showAMFSession,
}

type cliTask struct {
	Json     bool
	First    bool
	Continue bool
	Str      string
	Index    interface{}
}

func showAMFStatus(t *cliTask, Args []interface{}) {
	t.Str = "show amf status output"
}

func showAMFSession(t *cliTask, Args []interface{}) {
	t.Str = "show amf session output"
}

var parser *cmd.Node

func registerAPI(client rpc.RegisterClient) {
	var clis []rpc.RegisterRequest
	json.Unmarshal([]byte(cliSpec), &clis)

	parser = cmd.NewParser()

	for _, cli := range clis {
		cli.Module = "amf"
		cli.Privilege = 1
		cli.Code = rpc.ExecCode_REDIRECT_SHOW

		_, err := client.DoRegister(context.Background(), &cli)
		if err != nil {
			grpclog.Fatalf("client DoRegister failed: %v", err)
		}
		parser.InstallLine(cli.Line, cliMap[cli.Name])
	}
}

// Register module and API to openconfigd.
func register() {
	ch = make(chan interface{})
	for {
		conn, err := grpc.Dial(mgmtServerURL,
			grpc.WithInsecure(),
			grpc.FailOnNonTempDialError(true),
			grpc.WithBlock(),
			grpc.WithTimeout(time.Second*mgmtConnRetryInterval),
		)
		fmt.Println(conn, err)
		if err == nil {
			registerModule(rpc.NewRegisterClient(conn))
			registerAPI(rpc.NewRegisterClient(conn))
			for {
				<-ch
				break
			}
			conn.Close()
		} else {
			interval := rand.Intn(mgmtConnRetryInterval) + 1
			select {
			// Wait timeout.
			case <-time.After(time.Second * time.Duration(interval)):
			}
		}
	}
}

func (s *execServer) DoExec(_ context.Context, req *rpc.ExecRequest) (*rpc.ExecReply, error) {
	reply := new(rpc.ExecReply)
	if req.Type == rpc.ExecType_COMPLETE_DYNAMIC {
		// Fill in when dynamic completion is needed.
	}
	return reply, nil
}

type execModuleServer struct{}

func newExecModuleServer() *execModuleServer {
	return &execModuleServer{}
}

func (s *execModuleServer) DoExecModule(_ context.Context, req *rpc.ExecModuleRequest) (*rpc.ExecModuleReply, error) {
	reply := new(rpc.ExecModuleReply)
	return reply, nil
}

type execServer struct{}

func newExecServer() *execServer {
	return &execServer{}
}

type cliServer struct {
}

func newCliServer() *cliServer {
	return &cliServer{}
}

func newCliTask() *cliTask {
	return &cliTask{
		First: true,
	}
}

// Show is callback function invoked by gRPC event from openconfigd.
func (s *cliServer) Show(req *rpc.ShowRequest, stream rpc.Show_ShowServer) error {
	reply := &rpc.ShowReply{}

	result, fn, args, _ := parser.ParseLine(req.Line)
	if result != cmd.ParseSuccess || fn == nil {
		reply.Str = "% Command can't find: \"" + req.Line + "\"\n"
		err := stream.Send(reply)
		if err != nil {
			fmt.Println(err)
		}
		return nil
	}

	show := fn.(func(*cliTask, []interface{}))
	task := newCliTask()
	task.Json = req.Json
	for {
		task.Str = ""
		task.Continue = false
		show(task, args)
		task.First = false

		reply.Str = task.Str
		err := stream.Send(reply)
		if err != nil {
			fmt.Println(err)
			break
		}
		if !task.Continue {
			break
		}
	}
	return nil
}

func cliServe(grpcEndpoint string) error {
	lis, err := net.Listen("tcp", grpcEndpoint)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	rpc.RegisterExecServer(grpcServer, newExecServer())
	rpc.RegisterExecModuleServer(grpcServer, newExecModuleServer())
	rpc.RegisterShowServer(grpcServer, newCliServer())

	grpcServer.Serve(lis)
	return nil
}

// CfgMgr starts management services.
func CfgMgrStart() {
	go cliServe(":2701")
	go register()
}

// CfgMgrStop stops management services.
func CfgMgrStop() {
	lis.Close()
	close(ch)
}
