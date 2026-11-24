-- Kitty: billing reconciliation evidence (admin usage "audit" tab).
-- Same column as the Kitty main site (354_kitty_business_and_media.sql). Compact
-- token counters only; never request or response content. Nullable, metadata-only
-- ALTER (no table rewrite). Written asynchronously after the usage row is
-- persisted; billing never reads it. No index: reports always filter by time first.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS usage_audit jsonb;
