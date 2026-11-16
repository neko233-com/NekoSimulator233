package agent

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/migration"
)

func (s *Service) mumuMigrationPreflight(ctx context.Context, _ *mcp.CallToolRequest, request MuMuPreflightRequest) (*mcp.CallToolResult, migration.Report, error) {
	if err := migration.ValidateIndex(request.Index); err != nil {
		return nil, migration.Report{}, err
	}
	s.mu.Lock()
	m, err := s.manager()
	list := s.mumuList
	s.mu.Unlock()
	if err != nil {
		return nil, migration.Report{}, err
	}
	if list == nil {
		list = migration.ListLocal
	}
	report, err := migration.Check(ctx, request.Index, m.SDK, list)
	return nil, report, err
}
