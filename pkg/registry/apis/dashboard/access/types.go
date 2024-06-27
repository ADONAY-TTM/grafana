package access

import (
	"context"

	dashboardsV0 "github.com/grafana/grafana/pkg/apis/dashboard/v0alpha1"
	"github.com/grafana/grafana/pkg/storage/unified/resource"
)

// This does not check if you have permissions!

type DashboardQuery struct {
	OrgID    int64
	UID      string // to select a single dashboard
	Limit    int
	MaxBytes int
	MinID    int64 // from continue token

	FromHistory bool
	Version     int64

	// The label requirements
	Labels []*resource.Requirement
}

type DashboardAccess interface {
	resource.AppendingStore
	resource.BlobStore
	resource.ResourceSearchServer

	GetDashboard(ctx context.Context, orgId int64, uid string) (*dashboardsV0.Dashboard, int64, error)
	SaveDashboard(ctx context.Context, orgId int64, dash *dashboardsV0.Dashboard) (*dashboardsV0.Dashboard, bool, error)
	DeleteDashboard(ctx context.Context, orgId int64, uid string) (*dashboardsV0.Dashboard, bool, error)
}
