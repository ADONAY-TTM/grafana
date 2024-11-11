import grafanaAlertmanagerConfig from 'app/features/alerting/unified/mocks/server/entities/alertmanager-config/grafana-alertmanager-config';
import {
  ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1Matcher,
  ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1Route,
  ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1RoutingTree,
} from 'app/features/alerting/unified/openapi/routesApi.gen';
import { ROOT_ROUTE_NAME } from 'app/features/alerting/unified/utils/k8s/constants';
import { AlertManagerCortexConfig, MatcherOperator, Route } from 'app/plugins/datasource/alertmanager/types';

/**
 * Normalise matchers from config Route object -> what the k8s API expects to be returning
 */
const normalizeMatchers = (route: Route) => {
  const routeMatchers: ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1Matcher[] = [];

  if (route.object_matchers) {
    // todo foreach
    route.object_matchers.map(([label, type, value]) => {
      return { label, type, value };
    });
  }

  if (route.match_re) {
    Object.entries(route.match_re).forEach(([label, value]) => {
      routeMatchers.push({ label, type: MatcherOperator.regex, value });
    });
  }

  if (route.match) {
    Object.entries(route.match).forEach(([label, value]) => {
      routeMatchers.push({ label, type: MatcherOperator.equal, value });
    });
  }

  return routeMatchers;
};

const mapRoute = (route: Route): ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1Route => {
  const normalisedMatchers = normalizeMatchers(route);
  const { match, match_re, object_matchers, routes, receiver, ...rest } = route;
  return {
    ...rest,
    // TODO: Fix types in k8s API? Fix our types to not allow empty receiver? TBC
    receiver: receiver || '',
    matchers: normalisedMatchers,
    routes: routes ? routes.map(mapRoute) : undefined,
  };
};

export const getUserDefinedRoutingTree: (
  config: AlertManagerCortexConfig
) => ComGithubGrafanaGrafanaPkgApisAlertingNotificationsV0Alpha1RoutingTree = (config) => {
  const route = config.alertmanager_config?.route || {};

  const { routes, ...defaults } = route;

  return {
    metadata: {
      name: ROOT_ROUTE_NAME,
      namespace: 'default',
      annotations: {
        'grafana.com/provenance': 'none',
      },
    },
    spec: {
      defaults: { group_by: defaults.group_by || [], receiver: defaults.receiver! },
      routes:
        routes?.map((route) => {
          return mapRoute(route);
        }) || [],
    },
  };
};

export const ROUTING_TREE_MAP = new Map([[ROOT_ROUTE_NAME, getUserDefinedRoutingTree(grafanaAlertmanagerConfig)]]);
