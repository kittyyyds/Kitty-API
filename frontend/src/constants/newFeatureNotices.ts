/**
 * Stable notice IDs. Add an ID when announcing a feature, then reuse it beside
 * every related menu, tab, and setting. Dismissals are saved per admin in that
 * browser; use a new ID to announce the feature again in a later release.
 *
 * Only the announcements for features that exist in Kitty are listed here.
 */
export const NEW_FEATURE_NOTICES = {
  moderationUpstreamAccountScope: 'moderation-upstream-account-scope',
  keyRisk: 'key-risk-notices-acknowledgement-initial-announcement',
  keyRiskSources: 'key-risk-per-key-source-restriction-initial-announcement',
} as const
