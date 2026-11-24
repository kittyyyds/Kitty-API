-- Receipts survive usage log retention and freeze the account multiplier at request time.
-- Seed a small rollup for the paginated account list; do not recalculate procurement rules.
-- After loading historical rows without triggers, run tools/kitty/rebuild_account_costs.sql
-- before resuming traffic. Keep legacy-only rows whose source logs have expired.
-- Charges for logs pruned before any receipt was captured cannot be reconstructed.
SET LOCAL lock_timeout = '5s';
LOCK TABLE usage_logs, kitty_procurement_receipts IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE IF NOT EXISTS kitty_account_lifetime_costs (
    account_id bigint PRIMARY KEY,
    account_cost numeric NOT NULL,
    user_cost numeric NOT NULL
);
-- Rows written before procurement capture (or brought in without a receipt). Keep the
-- identity so a later receipt can replace it exactly once, even after log retention.
CREATE TABLE IF NOT EXISTS kitty_account_cost_legacy (
    api_key_id bigint NOT NULL,
    request_key text NOT NULL,
    account_id bigint NOT NULL,
    account_cost numeric NOT NULL,
    user_cost numeric NOT NULL,
    PRIMARY KEY (api_key_id,request_key)
);
INSERT INTO kitty_account_cost_legacy
SELECT u.api_key_id,k.request_key,u.account_id,
       COALESCE(u.account_stats_cost,u.total_cost,0)*COALESCE(u.account_rate_multiplier,1), COALESCE(u.actual_cost,0)
FROM usage_logs u
CROSS JOIN LATERAL (SELECT CASE WHEN COALESCE(u.request_id,'') <> '' THEN 'request:'||u.request_id ELSE 'legacy-row:'||u.id::text END AS request_key) k
WHERE NOT EXISTS (SELECT FROM kitty_procurement_receipts r WHERE (r.api_key_id,r.request_key)=(u.api_key_id,k.request_key))
ON CONFLICT (api_key_id,request_key) DO NOTHING;

INSERT INTO kitty_account_lifetime_costs
SELECT account_id,SUM(account_cost),SUM(user_cost) FROM (
    SELECT account_id,COALESCE((measurements->>'account_credits')::numeric,0) AS account_cost, COALESCE(request_valuation,0) AS user_cost FROM kitty_procurement_receipts
    UNION ALL SELECT account_id,account_cost,user_cost FROM kitty_account_cost_legacy
) AS history GROUP BY account_id
ON CONFLICT (account_id) DO UPDATE SET account_cost=EXCLUDED.account_cost, user_cost=EXCLUDED.user_cost;

CREATE OR REPLACE FUNCTION kitty_adjust_account_lifetime(a bigint,c numeric,u numeric) RETURNS void LANGUAGE sql AS $$
    INSERT INTO kitty_account_lifetime_costs(account_id,account_cost,user_cost) VALUES(a,c,u)
    ON CONFLICT(account_id) DO UPDATE SET account_cost=kitty_account_lifetime_costs.account_cost+EXCLUDED.account_cost,
        user_cost=kitty_account_lifetime_costs.user_cost+EXCLUDED.user_cost;
$$;
CREATE OR REPLACE FUNCTION kitty_account_cost_capture() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_account bigint; old_cost numeric:=0; old_user numeric:=0;
    new_cost numeric:=COALESCE((NEW.measurements->>'account_credits')::numeric,0); new_user numeric:=COALESCE(NEW.request_valuation,0);
BEGIN
    IF TG_OP='INSERT' THEN
        DELETE FROM kitty_account_cost_legacy WHERE (api_key_id,request_key)=(NEW.api_key_id,NEW.request_key)
            RETURNING account_id,account_cost,user_cost INTO old_account,old_cost,old_user;
    ELSE
        old_account:=OLD.account_id; old_cost:=COALESCE((OLD.measurements->>'account_credits')::numeric,0); old_user:=COALESCE(OLD.request_valuation,0);
    END IF;
    IF old_account = NEW.account_id THEN
        IF (new_cost,new_user) IS DISTINCT FROM (old_cost,old_user) THEN
            PERFORM kitty_adjust_account_lifetime(NEW.account_id,new_cost-old_cost,new_user-old_user);
        END IF;
    ELSE
        IF old_account IS NOT NULL THEN PERFORM kitty_adjust_account_lifetime(old_account,-old_cost,-old_user); END IF;
        PERFORM kitty_adjust_account_lifetime(NEW.account_id,new_cost,new_user);
    END IF;
    RETURN NULL;
END;
$$;
DROP TRIGGER IF EXISTS kitty_account_cost_receipt ON kitty_procurement_receipts;
CREATE TRIGGER kitty_account_cost_receipt AFTER INSERT OR UPDATE OF account_id,measurements,request_valuation ON kitty_procurement_receipts
FOR EACH ROW EXECUTE FUNCTION kitty_account_cost_capture();
