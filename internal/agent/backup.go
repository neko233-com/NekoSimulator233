package agent

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Service) exportBackup(ctx context.Context, _ *mcp.CallToolRequest, request BackupPath) (*mcp.CallToolResult, Result, error) {
	if !filepath.IsAbs(request.Path) {
		return nil, Result{}, errors.New("备份路径必须是绝对路径")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.manager()
	if err != nil {
		return nil, Result{}, err
	}
	err = m.ExportBackup(ctx, request.Path)
	return nil, Result{OK: err == nil}, err
}

func (s *Service) importBackup(ctx context.Context, _ *mcp.CallToolRequest, request RestoreBackup) (*mcp.CallToolResult, Result, error) {
	if !filepath.IsAbs(request.Path) {
		return nil, Result{}, errors.New("备份路径必须是绝对路径")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.manager()
	if err != nil {
		return nil, Result{}, err
	}
	target, err := m.WithName(request.Name)
	if err != nil {
		return nil, Result{}, err
	}
	err = target.ImportBackup(ctx, request.Path)
	return nil, Result{OK: err == nil}, err
}
