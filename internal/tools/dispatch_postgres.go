package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/ops"
)

func init() {
	register(toolname.EnsureSQLiteDatabase, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.EnsureSQLiteDatabase(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database provisioned."), nil
	})
}

func init() {
	register(toolname.GetSQLiteDatabaseStatus, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.GetSQLiteDatabaseStatus(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database status returned."), nil
	})
}

func init() {
	register(toolname.RemoveSQLiteDatabase, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.RemoveSQLiteDatabase(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		}, boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database removed."), nil
	})
}

func init() {
	register(toolname.ReconcilePostgreSQLService, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ReconcilePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service is ready"), nil
	})
}

func init() {
	register(toolname.GetPostgreSQLServiceStatus, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.GetPostgreSQLServiceStatus(ctx, postgresqlServiceArgs(args, binding))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service status returned"), nil
	})
}

func init() {
	register(toolname.RemovePostgreSQLService, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.RemovePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service was removed"), nil
	})
}

func init() {
	register(toolname.ReleasePostgreSQLServiceRelay, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ReleasePostgreSQLServiceRelay(stringField(args, "sessionId"), stringField(args, "relayToken"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service relay was released"), nil
	})
}
