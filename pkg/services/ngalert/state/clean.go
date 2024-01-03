package state

import (
	"context"
	"fmt"

	"github.com/grafana/grafana/pkg/infra/log"
	ngModels "github.com/grafana/grafana/pkg/services/ngalert/models"
)

// Clean removes superfluous and orphan instances from the database.
// It does not affect the manager's loaded cache. The manager will not load orphan states to begin with.
// If orphan states are present in the cache, they will be left there.
func (st *Manager) Clean(ctx context.Context, rules RuleReader) error {
	return Clean(ctx, st.instanceStore, rules, st.log)
}

// Clean removes orphan instances from `instances`, based on the rules given by `rules`.
func Clean(ctx context.Context, instances InstanceStore, rules RuleReader, logger log.Logger) error {
	orgIds, err := instances.FetchOrgIds(ctx)
	if err != nil {
		return fmt.Errorf("unable to fetch orgIDs: %w", err)
	}
	for _, orgID := range orgIds {
		ruleCmd := ngModels.ListAlertRulesQuery{
			OrgID: orgID,
		}
		orgRules, err := rules.ListAlertRules(ctx, &ruleCmd)
		if err != nil {
			logger.Error("unable to fetch previous state while cleaning instances", "error", err)
			continue
		}
		ruleUIDs := make(map[string]struct{})
		for _, r := range orgRules {
			ruleUIDs[r.UID] = struct{}{}
		}

		instanceRuleUIDs, err := instances.FetchRuleUIDs(ctx, orgID)
		if err != nil {
			logger.Error("unable to fetch previous state while cleaning instances", "error", err)
			continue
		}

		deletedRuleUIDs := make([]string, 0)
		for _, u := range instanceRuleUIDs {
			if _, ok := ruleUIDs[u]; !ok {
				deletedRuleUIDs = append(deletedRuleUIDs, u)
			}
		}

		for _, u := range deletedRuleUIDs {
			err := instances.DeleteAlertInstancesByRule(ctx, ngModels.AlertRuleKey{
				OrgID: orgID,
				UID:   u,
			})
			if err != nil {
				logger.Error("unable to clean orphan instances for rule", "uid", u)
				continue
			}
		}
	}

	return nil
}
