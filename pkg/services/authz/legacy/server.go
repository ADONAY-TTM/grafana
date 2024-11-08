package legacy

import (
	authzv1 "github.com/grafana/authlib/authz/proto/v1"
	authzextv1 "github.com/grafana/grafana/pkg/services/authz/proto/v1"

	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/tracing"
	"github.com/grafana/grafana/pkg/services/authz/legacy/store"
	"github.com/grafana/grafana/pkg/storage/legacysql"
)

type Server struct {
	authzv1.UnimplementedAuthzServiceServer
	authzextv1.UnimplementedAuthzExtentionServiceServer

	store  *store.Store
	logger log.Logger
	tracer tracing.Tracer
}

func NewServer(sql legacysql.LegacyDatabaseProvider, logger log.Logger, tracer tracing.Tracer) *Server {
	return &Server{
		store:  store.NewStore(sql),
		logger: logger,
		tracer: tracer,
	}
}
