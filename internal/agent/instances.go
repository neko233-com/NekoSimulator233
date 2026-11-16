package agent

import (
	"context"
	"errors"
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

func (s *Service) createInstance(ctx context.Context, _ *mcp.CallToolRequest, request InstanceName) (*mcp.CallToolResult, Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.manager()
	if err != nil {
		return nil, Result{}, err
	}
	err = m.CreateInstance(ctx, request.Name)
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
