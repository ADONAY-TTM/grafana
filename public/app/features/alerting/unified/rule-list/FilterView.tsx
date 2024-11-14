import { compact } from 'lodash';
import { useCallback, useEffect, useRef, useState } from 'react';
import Skeleton from 'react-loading-skeleton';
import { filter, from, map, merge, mergeMap, scan, Subject, take } from 'rxjs';

import { Button, Card, Stack } from '@grafana/ui';
import { Matcher } from 'app/plugins/datasource/alertmanager/types';
import { DataSourceRuleGroupIdentifier } from 'app/types/unified-alerting';
import { PromRuleDTO, PromRuleGroupDTO } from 'app/types/unified-alerting-dto';

import { prometheusApi } from '../api/prometheusApi';
import { RulesFilter } from '../search/rulesSearchParser';
import { labelsMatchMatchers } from '../utils/alertmanager';
import { getDatasourceAPIUid } from '../utils/datasource';
import { parseMatcher } from '../utils/matchers';
import { hashRule } from '../utils/rule-id';
import { isAlertingRule } from '../utils/rules';

import { AlertRuleLoader } from './RuleList.v2';
import { ListItem } from './components/ListItem';
import { ActionsLoader } from './components/RuleActionsButtons.V2';
import { RuleListIcon } from './components/RuleListIcon';

interface FilterViewProps {
  filterState: RulesFilter;
}

const MAX_RULE_GROUP_PAGE_SIZE = 2000;
const FRONTEND_PAGE_SIZE = 5;

export function FilterView({ filterState }: FilterViewProps) {
  const [rules, setRules] = useState<RuleWithOrigin[]>([]);
  const filteredRulesObservable = useFilteredRulesObservable(filterState);
  const filteredRulesObservableRef = useRef(filteredRulesObservable);
  const loadMoreSubject = useRef(new Subject());

  const [noMoreResults, _setNoMoreResults] = useState(false);

  const rulesLoader = loadMoreSubject.current.pipe(
    mergeMap(() => filteredRulesObservableRef.current),
    take(FRONTEND_PAGE_SIZE),
    scan((acc: RuleWithOrigin[], newRule) => [...acc, newRule], [])
  );

  useEffect(() => {
    const subscription = rulesLoader.subscribe((rules) => {
      setRules(rules);
    });

    return () => {
      subscription.unsubscribe();
    };
  }, []);

  // this function will load another page of items for the front-end
  const loadNextPage = useCallback(() => {
    loadMoreSubject.current?.next(undefined);
  }, []);

  useEffect(() => {
    loadNextPage();
  }, [loadNextPage]);

  const loading = false; // isLoading(state) || transitionPending;

  return (
    <Stack direction="column" gap={0}>
      {rules.map(({ ruleKey, rule, groupIdentifier }) => (
        <AlertRuleLoader key={ruleKey} rule={rule} groupIdentifier={groupIdentifier} />
      ))}
      {loading ? (
        <>
          <AlertRuleListItemLoader />
          <AlertRuleListItemLoader />
          <AlertRuleListItemLoader />
        </>
      ) : noMoreResults ? (
        <Card>No more results</Card>
      ) : (
        <Button onClick={() => loadNextPage()}>Load more...</Button>
      )}
    </Stack>
  );
}

const AlertRuleListItemLoader = () => (
  <ListItem
    title={<Skeleton width={64} />}
    icon={<RuleListIcon isPaused={false} />}
    description={<Skeleton width={256} />}
    actions={<ActionsLoader />}
  />
);

const { useLazyGroupsQuery } = prometheusApi;

interface RuleWithOrigin {
  /**
   * Artificial frontend-only identifier for the rule.
   * It's used as a key for the rule in the rule list to prevent key duplication
   */
  ruleKey: string;
  rule: PromRuleDTO;
  groupIdentifier: DataSourceRuleGroupIdentifier;
}

interface GroupWithIdentifier extends PromRuleGroupDTO {
  identifier: DataSourceRuleGroupIdentifier;
}

function useFilteredRulesObservable(filterState: RulesFilter) {
  const [fetchGroups] = useLazyGroupsQuery();

  // this function will return an async generator that returns a tuple of [rulesSourceName, PrometheusRuleGroup]
  const fetchGroupsForRulesSource = useCallback(
    async function* (ruleSourceName: string, maxGroups: number) {
      const ruleSourceUid = getDatasourceAPIUid(ruleSourceName);

      const response = await fetchGroups({
        ruleSource: { uid: ruleSourceUid },
        groupLimit: maxGroups,
      });

      if (response.data?.data) {
        yield* response.data.data.groups.map((group) => [ruleSourceName, group] as const);
      }

      let lastToken: string | undefined = undefined;
      if (response.data?.data?.groupNextToken) {
        lastToken = response.data.data.groupNextToken;
      }

      while (lastToken) {
        const response = await fetchGroups({
          ruleSource: { uid: ruleSourceUid },
          groupNextToken: lastToken,
          groupLimit: maxGroups,
        });

        if (response.data?.data) {
          yield* response.data.data.groups.map((group) => [ruleSourceName, group] as const);
        }

        lastToken = response.data?.data?.groupNextToken;
      }
    },
    [fetchGroups]
  );

  // this function will return a Observable that emits rule groups.
  // It will keep taking pages from the data source until all subscriber's conditions are met.
  const fetchGroupsFromAllRuleSources = useCallback(
    (rulesSourceNames: string[], maxGroups: number) => {
      // create an array of observables that will use the async generator above
      const observables = rulesSourceNames.map((ruleSourceName) =>
        from(fetchGroupsForRulesSource(ruleSourceName, maxGroups))
      );

      // merge them so we create a new observable that emits whenever we have a rule group from _any_ data source
      return merge(...observables);
    },
    [fetchGroupsForRulesSource]
  );

  const ruleGroupsObservable = fetchGroupsFromAllRuleSources(filterState.dataSourceNames, MAX_RULE_GROUP_PAGE_SIZE);

  const filteredRulesObservable = ruleGroupsObservable.pipe(
    filter(([_, group]) => filterRuleGroup(group, filterState)),
    map(([ruleSourceName, group]) => mapGroupToGroupWithIdentifier(group, ruleSourceName)),
    mergeMap((group) => mapGroupToRules(group)),
    filter((rule) => ruleMatchesFilter(rule.rule, filterState))
  );

  return filteredRulesObservable;
}

function mapGroupToRules(group: GroupWithIdentifier): RuleWithOrigin[] {
  const groupKey = `${group.identifier.namespace.name}${group.identifier.groupName}`;
  return group.rules.map<RuleWithOrigin>((rule) => ({
    ruleKey: `${group.identifier.rulesSource.name}-${groupKey}-${hashRule(rule)}`,
    rule,
    groupIdentifier: group.identifier,
  }));
}

function mapGroupToGroupWithIdentifier(group: PromRuleGroupDTO, ruleSourceName: string): GroupWithIdentifier {
  return {
    ...group,
    identifier: {
      rulesSource: { name: ruleSourceName, uid: getDatasourceAPIUid(ruleSourceName) },
      namespace: { name: group.file },
      groupName: group.name,
      groupOrigin: 'datasource',
    },
  };
}

/**
 * Returns a new group with only the rules that match the filter.
 * @returns A new group with filtered rules, or undefined if the group does not match the filter or all rules are filtered out.
 */
function filterRuleGroup<T extends PromRuleGroupDTO>(group: T, filterState: RulesFilter): boolean {
  const { name, file } = group;

  // TODO Add fuzzy filtering or not
  if (filterState.namespace && !file.includes(filterState.namespace)) {
    return false;
  }

  if (filterState.groupName && !name.includes(filterState.groupName)) {
    return false;
  }

  return true;
}

function ruleMatchesFilter(rule: PromRuleDTO, filterState: RulesFilter) {
  const { name, labels = {}, health, type } = rule;

  if (filterState.freeFormWords.length > 0 && !filterState.freeFormWords.some((word) => name.includes(word))) {
    return false;
  }
  if (filterState.ruleName && !name.includes(filterState.ruleName)) {
    return false;
  }
  if (filterState.labels.length > 0) {
    const matchers = compact(filterState.labels.map(looseParseMatcher));
    const doRuleLabelsMatchQuery = matchers.length > 0 && labelsMatchMatchers(labels, matchers);
    if (!doRuleLabelsMatchQuery) {
      return false;
    }
  }
  if (filterState.ruleType && type !== filterState.ruleType) {
    return false;
  }
  if (filterState.ruleState) {
    if (!isAlertingRule(rule)) {
      return false;
    }
    if (rule.state !== filterState.ruleState) {
      return false;
    }
  }
  if (filterState.ruleHealth && health !== filterState.ruleHealth) {
    return false;
  }

  return true;
}

function looseParseMatcher(matcherQuery: string): Matcher | undefined {
  try {
    return parseMatcher(matcherQuery);
  } catch {
    // Try to createa a matcher than matches all values for a given key
    return { name: matcherQuery, value: '', isRegex: true, isEqual: true };
  }
}
