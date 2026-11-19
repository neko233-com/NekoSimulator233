package agent

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Service) listInstances(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, InstanceList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.manager()
	if err != nil {
		return nil, InstanceList{}, err
	}
	instances, err := m.ListInstances(ctx)
	return nil, InstanceList{Instances: instances, Selected: m.Name}, err
}

func (s *Service) createInstance(ctx context.Context, req *mcp.CallToolRequest, request CreateInstance) (*mcp.CallToolResult, Result, error) {
	s.mu.Lock()
	m, err := s.manager()
	s.mu.Unlock()
	if err != nil {
		return nil, Result{}, err
	}
	var notify func(int)
	if req != nil && req.Params != nil && req.Params.GetProgressToken() != nil {
		token := req.Params.GetProgressToken()
		notify = func(percent int) {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: float64(percent), Total: 100, Message: "准备 Google Play 系统镜像"})
		}
	}
	if os.Getenv("NEKO_PREFER_USERDEBUG_IMAGE") == "1" {
		err = m.CreateInstanceAPI(ctx, request.Name, request.API)
	} else {
		err = m.CreatePlayInstance(ctx, request.Name, request.API, notify)
	}
	return nil, Result{OK: err == nil}, err
}

func (s *Service) selectInstance(ctx context.Context, _ *mcp.CallToolRequest, request InstanceName) (*mcp.CallToolResult, InstanceName, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.manager()
	if err != nil {
		return nil, InstanceName{}, err
	}
	if _, err = m.WithName(request.Name); err != nil {
		return nil, InstanceName{}, err
	}
	instances, err := m.ListInstances(ctx)
	if err != nil {
		return nil, InstanceName{}, err
	}
	for _, instance := range instances {
		if strings.EqualFold(instance.Name, request.Name) {
			if instance.State == "unknown" {
				return nil, InstanceName{}, errors.New("实例状态未知或配置不完整，未切换")
			}
			s.selected = instance.Name
			return nil, InstanceName{Name: instance.Name}, nil
		}
	}
	return nil, InstanceName{}, errors.New("实例不存在，未切换")
}
