-- Kitty: staged group usage rollups without changing source retention or measures.
SET LOCAL lock_timeout = '5s';

ALTER TABLE usage_group_rollup_state
    ADD COLUMN IF NOT EXISTS building_until DATE,
    ADD COLUMN IF NOT EXISTS build_dirty_from DATE;

COMMENT ON COLUMN usage_group_rollup_state.building_until IS '进行中重建的目标日期（排他上界）；没有重建时为 NULL。';
COMMENT ON COLUMN usage_group_rollup_state.build_dirty_from IS '重建期间源记录发生变化的最早日期；发布水位不越过该日期。';

-- 按受影响日期后退发布水位，并在重建进行中记录脏日期；水位与脏日期都已不晚于该日期时不写状态行。
-- 调用方必须已对状态行持有 FOR KEY SHARE 或更强的行锁：重建的登记与发布持有 FOR UPDATE，
-- 由此保证每次源记录变更要么先于登记提交、被重建快照看到，要么看到登记并记入脏日期。
CREATE OR REPLACE FUNCTION apply_group_usage_rollup_invalidation(affected_date DATE)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE usage_group_rollup_state
    SET closed_before = LEAST(closed_before, affected_date),
        build_dirty_from = CASE
            WHEN building_until > affected_date THEN LEAST(build_dirty_from, affected_date)
            ELSE build_dirty_from
        END,
        updated_at = NOW()
    WHERE id = 1
        AND (
            closed_before > affected_date
            OR (
                building_until > affected_date
                AND (build_dirty_from IS NULL OR build_dirty_from > affected_date)
            )
        );
END;
$$;

-- Keep the existing financial retention boundary; only invalidate the affected date.
CREATE OR REPLACE FUNCTION invalidate_group_usage_rollups_at(affected_at TIMESTAMPTZ, bucket_timezone TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF affected_at IS NULL THEN
        RETURN;
    END IF;
    PERFORM id FROM usage_group_rollup_state WHERE id = 1 FOR KEY SHARE;
    PERFORM apply_group_usage_rollup_invalidation((affected_at AT TIME ZONE bucket_timezone)::date);
END;
$$;

-- DELETE/UPDATE 行级触发器。FOR KEY SHARE 与 usage_logs 写入及其他失效互相兼容，只与重建的登记、发布串行。
CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    configured_timezone TEXT := current_setting('TimeZone');
    old_date DATE;
    new_date DATE;
    affected_date DATE;
    state_closed_before DATE;
    state_building_until DATE;
    state_build_dirty_from DATE;
BEGIN
    SELECT closed_before, building_until, build_dirty_from
    INTO state_closed_before, state_building_until, state_build_dirty_from
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR KEY SHARE;

    IF FOUND THEN
        IF OLD.group_id IS NOT NULL THEN
            old_date := (OLD.created_at AT TIME ZONE configured_timezone)::date;
        END IF;
        IF TG_OP = 'UPDATE' THEN
            IF NEW.group_id IS NOT NULL THEN
                new_date := (NEW.created_at AT TIME ZONE configured_timezone)::date;
            END IF;
        END IF;

        affected_date := LEAST(old_date, new_date);
        IF state_closed_before > affected_date
            OR (
                state_building_until > affected_date
                AND (state_build_dirty_from IS NULL OR state_build_dirty_from > affected_date)
            ) THEN
            PERFORM apply_group_usage_rollup_invalidation(affected_date);
        END IF;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

-- INSERT 语句级触发器：每个批量 INSERT 只读一次状态行；当天写入既不后退水位也不记脏。
CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    configured_timezone TEXT := current_setting('TimeZone');
    affected_date DATE;
    state_closed_before DATE;
    state_building_until DATE;
    state_build_dirty_from DATE;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM inserted_usage_logs WHERE group_id IS NOT NULL) THEN
        RETURN NULL;
    END IF;

    SELECT closed_before, building_until, build_dirty_from
    INTO state_closed_before, state_building_until, state_build_dirty_from
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR KEY SHARE;

    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM inserted_usage_logs
    WHERE group_id IS NOT NULL;

    IF state_closed_before > affected_date
        OR (
            state_building_until > affected_date
            AND (state_build_dirty_from IS NULL OR state_build_dirty_from > affected_date)
        ) THEN
        PERFORM apply_group_usage_rollup_invalidation(affected_date);
    END IF;

    RETURN NULL;
END;
$$;
