package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

func init() {
	register(toolname.EnsureSQLiteDatabase, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().EnsureSQLiteDatabase(ctx, postgres.SQLiteDatabaseArgs{
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
	register(toolname.GetSQLiteDatabaseStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().GetSQLiteDatabaseStatus(ctx, postgres.SQLiteDatabaseArgs{
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
	register(toolname.RemoveSQLiteDatabase, EffectDestructive, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().RemoveSQLiteDatabase(ctx, postgres.SQLiteDatabaseArgs{
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
	register(toolname.ReconcilePostgreSQLService, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().ReconcilePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service is ready"), nil
	})
}

func init() {
	register(toolname.GetPostgreSQLServiceStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().GetPostgreSQLServiceStatus(ctx, postgresqlServiceArgs(args, binding))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service status returned"), nil
	})
}

func init() {
	register(toolname.RemovePostgreSQLService, EffectDestructive, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().RemovePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service was removed"), nil
	})
}

func init() {
	register(toolname.ReleasePostgreSQLServiceRelay, EffectCredential, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().ReleasePostgreSQLServiceRelay(stringField(args, "sessionId"), stringField(args, "relayToken"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service relay was released"), nil
	})
}
