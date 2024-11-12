import { useMemo } from 'react';

import { contextSrv as ctx } from 'app/core/services/context_srv';
import { useFolder } from 'app/features/alerting/unified/hooks/useFolder';
import { AlertmanagerChoice } from 'app/plugins/datasource/alertmanager/types';
import { AccessControlAction } from 'app/types';
import { CombinedRule, RuleGroupIdentifierV2 } from 'app/types/unified-alerting';
import { RulerRuleDTO } from 'app/types/unified-alerting-dto';

import { alertmanagerApi } from '../api/alertmanagerApi';
import { useAlertmanager } from '../state/AlertmanagerContext';
import { getInstancesPermissions, getNotificationsPermissions, getRulesPermissions } from '../utils/access-control';
import { getRulesSourceName } from '../utils/datasource';
import { isAdmin } from '../utils/misc';
import { isFederatedRuleGroup, isGrafanaRecordingRule, isGrafanaRulerRule, isPluginProvidedRule } from '../utils/rules';

import { useIsRuleEditable } from './useIsRuleEditable';

/**
 * These hooks will determine if
 *  1. the action is supported in the current context (alertmanager, alert rule or general context)
 *  2. user is allowed to perform actions based on their set of permissions / assigned role
 */

// this enum lists all of the available actions we can perform within the context of an alertmanager
export enum AlertmanagerAction {
  // configuration
  ViewExternalConfiguration = 'view-external-configuration',
  UpdateExternalConfiguration = 'update-external-configuration',

  // contact points
  CreateContactPoint = 'create-contact-point',
  ViewContactPoint = 'view-contact-point',
  UpdateContactPoint = 'edit-contact-points',
  DeleteContactPoint = 'delete-contact-point',
  ExportContactPoint = 'export-contact-point',

  // notification templates
  CreateNotificationTemplate = 'create-notification-template',
  ViewNotificationTemplate = 'view-notification-template',
  UpdateNotificationTemplate = 'edit-notification-template',
  DeleteNotificationTemplate = 'delete-notification-template',
  DecryptSecrets = 'decrypt-secrets',

  // notification policies
  CreateNotificationPolicy = 'create-notification-policy',
  ViewNotificationPolicyTree = 'view-notification-policy-tree',
  UpdateNotificationPolicyTree = 'update-notification-policy-tree',
  DeleteNotificationPolicy = 'delete-notification-policy',
  ExportNotificationPolicies = 'export-notification-policies',
  ViewAutogeneratedPolicyTree = 'view-autogenerated-policy-tree',

  // silences – these cannot be deleted only "expired" (updated)
  CreateSilence = 'create-silence',
  ViewSilence = 'view-silence',
  UpdateSilence = 'update-silence',
  PreviewSilencedInstances = 'preview-silenced-alerts',

  // mute timings
  ViewMuteTiming = 'view-mute-timing',
  CreateMuteTiming = 'create-mute-timing',
  UpdateMuteTiming = 'update-mute-timing',
  DeleteMuteTiming = 'delete-mute-timing',
  ExportMuteTimings = 'export-mute-timings',
}

// this enum lists all of the available actions we can take on a single alert rule
export enum AlertRuleAction {
  Duplicate = 'duplicate-alert-rule',
  View = 'view-alert-rule',
  Update = 'update-alert-rule',
  Delete = 'delete-alert-rule',
  Explore = 'explore-alert-rule',
  Silence = 'silence-alert-rule',
  ModifyExport = 'modify-export-rule',
  Pause = 'pause-alert-rule',
}

// this enum lists all of the actions we can perform within alerting in general, not linked to a specific
// alert source, rule or alertmanager
export enum AlertingAction {
  // internal (Grafana managed)
  CreateAlertRule = 'create-alert-rule',
  ViewAlertRule = 'view-alert-rule',
  UpdateAlertRule = 'update-alert-rule',
  DeleteAlertRule = 'delete-alert-rule',
  ExportGrafanaManagedRules = 'export-grafana-managed-rules',
  ReadConfigurationStatus = 'read-configuration-status',

  // external (any compatible alerting data source)
  CreateExternalAlertRule = 'create-external-alert-rule',
  ViewExternalAlertRule = 'view-external-alert-rule',
  UpdateExternalAlertRule = 'update-external-alert-rule',
  DeleteExternalAlertRule = 'delete-external-alert-rule',
}

// these just makes it easier to read the code :)
const AlwaysSupported = true;
const NotSupported = false;

export type Action = AlertmanagerAction | AlertingAction | AlertRuleAction;
export type Ability = [actionSupported: boolean, actionAllowed: boolean];
export type Abilities<T extends Action> = Record<T, Ability>;

/**
 * This one will check for alerting abilities that don't apply to any particular alert source or alert rule
 */
export const useAlertingAbilities = (): Abilities<AlertingAction> => {
  return {
    // internal (Grafana managed)
    [AlertingAction.CreateAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleCreate),
    [AlertingAction.ViewAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleRead),
    [AlertingAction.UpdateAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleUpdate),
    [AlertingAction.DeleteAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleDelete),
    [AlertingAction.ExportGrafanaManagedRules]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleRead),
    [AlertingAction.ReadConfigurationStatus]: [
      AlwaysSupported,
      ctx.hasPermission(AccessControlAction.AlertingInstanceRead) ||
        ctx.hasPermission(AccessControlAction.AlertingNotificationsRead),
    ],

    // external
    [AlertingAction.CreateExternalAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleExternalWrite),
    [AlertingAction.ViewExternalAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleExternalRead),
    [AlertingAction.UpdateExternalAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleExternalWrite),
    [AlertingAction.DeleteExternalAlertRule]: toAbility(AlwaysSupported, AccessControlAction.AlertingRuleExternalWrite),
  };
};

export const useAlertingAbility = (action: AlertingAction): Ability => {
  const allAbilities = useAlertingAbilities();
  return allAbilities[action];
};

/**
 * This hook will check if we support the action and have sufficient permissions for it on a single alert rule
 */
export function useAlertRuleAbility(rule: CombinedRule, action: AlertRuleAction): Ability {
  const abilities = useAllAlertRuleAbilities(rule);

  return useMemo(() => {
    return abilities[action];
  }, [abilities, action]);
}

export function useAlertRuleAbilities(rule: CombinedRule, actions: AlertRuleAction[]): Ability[] {
  const abilities = useAllAlertRuleAbilities(rule);

  return useMemo(() => {
    return actions.map((action) => abilities[action]);
  }, [abilities, actions]);
}

export function useRulerRuleAbility(
  rule: RulerRuleDTO | undefined,
  groupIdentifier: RuleGroupIdentifierV2,
  action: AlertRuleAction
): Ability {
  const abilities = useAllRulerRuleAbilities(rule, groupIdentifier);

  return useMemo(() => {
    return abilities[action];
  }, [abilities, action]);
}

export function useRulerRuleAbilities(
  rule: RulerRuleDTO,
  groupIdentifier: RuleGroupIdentifierV2,
  actions: AlertRuleAction[]
): Ability[] {
  const abilities = useAllRulerRuleAbilities(rule, groupIdentifier);

  return useMemo(() => {
    return actions.map((action) => abilities[action]);
  }, [abilities, actions]);
}

// This hook is being called a lot in different places
// In some cases multiple times for ~80 rules (e.g. on the list page)
// We need to investigate further if some of these calls are redundant
// In the meantime, memoizing the result helps
export function useAllAlertRuleAbilities(rule: CombinedRule): Abilities<AlertRuleAction> {
  const rulesSourceName = getRulesSourceName(rule.namespace.rulesSource);

  const {
    isEditable,
    isRemovable,
    isRulerAvailable = false,
    loading,
  } = useIsRuleEditable(rulesSourceName, rule.rulerRule);
  const [_, exportAllowed] = useAlertingAbility(AlertingAction.ExportGrafanaManagedRules);
  const canSilence = useCanSilence(rule.rulerRule);

  const abilities = useMemo<Abilities<AlertRuleAction>>(() => {
    const isProvisioned = isGrafanaRulerRule(rule.rulerRule) && Boolean(rule.rulerRule.grafana_alert.provenance);
    const isFederated = isFederatedRuleGroup(rule.group);
    const isGrafanaManagedAlertRule = isGrafanaRulerRule(rule.rulerRule);
    const isPluginProvided = isPluginProvidedRule(rule.rulerRule);

    // if a rule is either provisioned, federated or provided by a plugin rule, we don't allow it to be removed or edited
    const immutableRule = isProvisioned || isFederated || isPluginProvided;

    // while we gather info, pretend it's not supported
    const MaybeSupported = loading ? NotSupported : isRulerAvailable;
    const MaybeSupportedUnlessImmutable = immutableRule ? NotSupported : MaybeSupported;

    // Creating duplicates of plugin-provided rules does not seem to make a lot of sense
    const duplicateSupported = isPluginProvided ? NotSupported : MaybeSupported;

    const rulesPermissions = getRulesPermissions(rulesSourceName);

    const abilities: Abilities<AlertRuleAction> = {
      [AlertRuleAction.Duplicate]: toAbility(duplicateSupported, rulesPermissions.create),
      [AlertRuleAction.View]: toAbility(AlwaysSupported, rulesPermissions.read),
      [AlertRuleAction.Update]: [MaybeSupportedUnlessImmutable, isEditable ?? false],
      [AlertRuleAction.Delete]: [MaybeSupportedUnlessImmutable, isRemovable ?? false],
      [AlertRuleAction.Explore]: toAbility(AlwaysSupported, AccessControlAction.DataSourcesExplore),
      [AlertRuleAction.Silence]: canSilence,
      [AlertRuleAction.ModifyExport]: [isGrafanaManagedAlertRule, exportAllowed],
      [AlertRuleAction.Pause]: [MaybeSupportedUnlessImmutable && isGrafanaManagedAlertRule, isEditable ?? false],
    };

    return abilities;
  }, [rule, loading, isRulerAvailable, isEditable, isRemovable, rulesSourceName, exportAllowed, canSilence]);

  return abilities;
}

export function useAllRulerRuleAbilities(
  rule: RulerRuleDTO | undefined,
  groupIdentifier: RuleGroupIdentifierV2
): Abilities<AlertRuleAction> {
  const rulesSourceName = groupIdentifier.rulesSource.name;

  const { isEditable, isRemovable, isRulerAvailable = false, loading } = useIsRuleEditable(rulesSourceName, rule);
  const [_, exportAllowed] = useAlertingAbility(AlertingAction.ExportGrafanaManagedRules);
  const canSilence = useCanSilence(rule);

  const abilities = useMemo<Abilities<AlertRuleAction>>(() => {
    const isProvisioned = isGrafanaRulerRule(rule) && Boolean(rule.grafana_alert.provenance);
    // const isFederated = isFederatedRuleGroup();
    const isFederated = false;
    const isGrafanaManagedAlertRule = isGrafanaRulerRule(rule);
    const isPluginProvided = isPluginProvidedRule(rule);

    // if a rule is either provisioned, federated or provided by a plugin rule, we don't allow it to be removed or edited
    const immutableRule = isProvisioned || isFederated || isPluginProvided;

    // while we gather info, pretend it's not supported
    const MaybeSupported = loading ? NotSupported : isRulerAvailable;
    const MaybeSupportedUnlessImmutable = immutableRule ? NotSupported : MaybeSupported;

    // Creating duplicates of plugin-provided rules does not seem to make a lot of sense
    const duplicateSupported = isPluginProvided ? NotSupported : MaybeSupported;

    const rulesPermissions = getRulesPermissions(rulesSourceName);

    const abilities: Abilities<AlertRuleAction> = {
      [AlertRuleAction.Duplicate]: toAbility(duplicateSupported, rulesPermissions.create),
      [AlertRuleAction.View]: toAbility(AlwaysSupported, rulesPermissions.read),
      [AlertRuleAction.Update]: [MaybeSupportedUnlessImmutable, isEditable ?? false],
      [AlertRuleAction.Delete]: [MaybeSupportedUnlessImmutable, isRemovable ?? false],
      [AlertRuleAction.Explore]: toAbility(AlwaysSupported, AccessControlAction.DataSourcesExplore),
      [AlertRuleAction.Silence]: canSilence,
      [AlertRuleAction.ModifyExport]: [isGrafanaManagedAlertRule, exportAllowed],
      [AlertRuleAction.Pause]: [MaybeSupportedUnlessImmutable && isGrafanaManagedAlertRule, isEditable ?? false],
    };

    return abilities;
  }, [rule, loading, isRulerAvailable, rulesSourceName, isEditable, isRemovable, canSilence, exportAllowed]);

  return abilities;
}

export function useAllAlertmanagerAbilities(): Abilities<AlertmanagerAction> {
  const {
    selectedAlertmanager,
    hasConfigurationAPI,
    isGrafanaAlertmanager: isGrafanaFlavoredAlertmanager,
  } = useAlertmanager();

  // These are used for interacting with Alertmanager resources where we apply alert.notifications:<name> permissions.
  // There are different permissions based on wether the built-in alertmanager is selected (grafana) or an external one.
  const notificationsPermissions = getNotificationsPermissions(selectedAlertmanager!);
  const instancePermissions = getInstancesPermissions(selectedAlertmanager!);

  // list out all of the abilities, and if the user has permissions to perform them
  const abilities: Abilities<AlertmanagerAction> = {
    // -- configuration --
    [AlertmanagerAction.ViewExternalConfiguration]: toAbility(
      AlwaysSupported,
      AccessControlAction.AlertingNotificationsExternalRead
    ),
    [AlertmanagerAction.UpdateExternalConfiguration]: toAbility(
      hasConfigurationAPI,
      AccessControlAction.AlertingNotificationsExternalWrite
    ),
    // -- contact points --
    [AlertmanagerAction.CreateContactPoint]: toAbility(
      hasConfigurationAPI,
      notificationsPermissions.create,
      // TODO: Move this into the permissions config and generalise that code to allow for an array of permissions
      isGrafanaFlavoredAlertmanager ? AccessControlAction.AlertingReceiversCreate : null
    ),
    [AlertmanagerAction.ViewContactPoint]: toAbility(AlwaysSupported, notificationsPermissions.read),
    [AlertmanagerAction.UpdateContactPoint]: toAbility(hasConfigurationAPI, notificationsPermissions.update),
    [AlertmanagerAction.DeleteContactPoint]: toAbility(hasConfigurationAPI, notificationsPermissions.delete),
    // At the time of writing, only Grafana flavored alertmanager supports exporting,
    // and if a user can view the contact point, then they can also export it
    // So the only check we make is if the alertmanager is Grafana flavored
    [AlertmanagerAction.ExportContactPoint]: [isGrafanaFlavoredAlertmanager, isGrafanaFlavoredAlertmanager],
    // -- notification templates --
    [AlertmanagerAction.CreateNotificationTemplate]: toAbility(hasConfigurationAPI, notificationsPermissions.create),
    [AlertmanagerAction.ViewNotificationTemplate]: toAbility(AlwaysSupported, notificationsPermissions.read),
    [AlertmanagerAction.UpdateNotificationTemplate]: toAbility(hasConfigurationAPI, notificationsPermissions.update),
    [AlertmanagerAction.DeleteNotificationTemplate]: toAbility(hasConfigurationAPI, notificationsPermissions.delete),
    // -- notification policies --
    [AlertmanagerAction.CreateNotificationPolicy]: toAbility(hasConfigurationAPI, notificationsPermissions.create),
    [AlertmanagerAction.ViewNotificationPolicyTree]: toAbility(AlwaysSupported, notificationsPermissions.read),
    [AlertmanagerAction.UpdateNotificationPolicyTree]: toAbility(hasConfigurationAPI, notificationsPermissions.update),
    [AlertmanagerAction.DeleteNotificationPolicy]: toAbility(hasConfigurationAPI, notificationsPermissions.delete),
    [AlertmanagerAction.ExportNotificationPolicies]: toAbility(
      isGrafanaFlavoredAlertmanager,
      notificationsPermissions.read
    ),
    [AlertmanagerAction.DecryptSecrets]: toAbility(
      isGrafanaFlavoredAlertmanager,
      notificationsPermissions.provisioning.readSecrets
    ),
    [AlertmanagerAction.ViewAutogeneratedPolicyTree]: [isGrafanaFlavoredAlertmanager, isAdmin()],
    // -- silences --
    // for now, all supported Alertmanager flavors have API endpoints for managing silences
    [AlertmanagerAction.CreateSilence]: toAbility(AlwaysSupported, instancePermissions.create),
    [AlertmanagerAction.ViewSilence]: toAbility(AlwaysSupported, instancePermissions.read),
    [AlertmanagerAction.UpdateSilence]: toAbility(AlwaysSupported, instancePermissions.update),
    [AlertmanagerAction.PreviewSilencedInstances]: toAbility(AlwaysSupported, instancePermissions.read),
    // -- mute timtings --
    [AlertmanagerAction.CreateMuteTiming]: toAbility(hasConfigurationAPI, notificationsPermissions.create),
    [AlertmanagerAction.ViewMuteTiming]: toAbility(AlwaysSupported, notificationsPermissions.read),
    [AlertmanagerAction.UpdateMuteTiming]: toAbility(hasConfigurationAPI, notificationsPermissions.update),
    [AlertmanagerAction.DeleteMuteTiming]: toAbility(hasConfigurationAPI, notificationsPermissions.delete),
    [AlertmanagerAction.ExportMuteTimings]: toAbility(isGrafanaFlavoredAlertmanager, notificationsPermissions.read),
  };

  return abilities;
}

export function useAlertmanagerAbility(action: AlertmanagerAction): Ability {
  const abilities = useAllAlertmanagerAbilities();

  return useMemo(() => {
    return abilities[action];
  }, [abilities, action]);
}

export function useAlertmanagerAbilities(actions: AlertmanagerAction[]): Ability[] {
  const abilities = useAllAlertmanagerAbilities();

  return useMemo(() => {
    return actions.map((action) => abilities[action]);
  }, [abilities, actions]);
}

const { useGetGrafanaAlertingConfigurationStatusQuery } = alertmanagerApi;
/**
 * We don't want to show the silence button if either
 * 1. the user has no permissions to create silences
 * 2. the admin has configured to only send instances to external AMs
 */
function useCanSilence(rule?: RulerRuleDTO): [boolean, boolean] {
  const folderUID = isGrafanaRulerRule(rule) ? rule.grafana_alert.namespace_uid : undefined;
  const { loading: folderIsLoading, folder } = useFolder(folderUID);

  const isGrafanaManagedRule = rule && isGrafanaRulerRule(rule);
  const isGrafanaRecording = rule && isGrafanaRecordingRule(rule);

  const { currentData: amConfigStatus, isLoading } = useGetGrafanaAlertingConfigurationStatusQuery(undefined, {
    skip: !isGrafanaManagedRule || !rule,
  });

  if (!rule) {
    return [false, false];
  }

  // we don't support silencing when the rule is not a Grafana managed alerting rule
  // we simply don't know what Alertmanager the ruler is sending alerts to
  if (!isGrafanaManagedRule || isGrafanaRecording || isLoading || folderIsLoading || !folder) {
    return [false, false];
  }

  const interactsOnlyWithExternalAMs = amConfigStatus?.alertmanagersChoice === AlertmanagerChoice.External;
  const interactsWithAll = amConfigStatus?.alertmanagersChoice === AlertmanagerChoice.All;
  const silenceSupported = !interactsOnlyWithExternalAMs || interactsWithAll;

  const { accessControl = {} } = folder;

  // User is permitted to silence if they either have the "global" permissions of "AlertingInstanceCreate",
  // or the folder specific access control of "AlertingSilenceCreate"
  const allowedToSilence = Boolean(
    ctx.hasPermission(AccessControlAction.AlertingInstanceCreate) ||
      accessControl[AccessControlAction.AlertingSilenceCreate]
  );

  return [silenceSupported, allowedToSilence];
}

// just a convenient function
const toAbility = (supported: boolean, ...actions: Array<AccessControlAction | null>): Ability => [
  supported,
  actions.some((action) => action && ctx.hasPermission(action)),
];
