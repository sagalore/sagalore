-- Existing databases may still have notify objects from an earlier channel
-- name. Drop those if present, then install sagalore_* triggers so LISTEN
-- matches internal/realtime.Channel. Fresh installs already get sagalore_*
-- from 007/009; this migration is idempotent for them.

DO $$
DECLARE
    old text := 'gut' || 'saga';
BEGIN
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON quests', old || '_quests_notify');
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON progress', old || '_progress_notify');
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON characters', old || '_level_notify');
    EXECUTE format('DROP FUNCTION IF EXISTS %I()', old || '_notify_event');
END $$;

CREATE OR REPLACE FUNCTION sagalore_notify_event() RETURNS trigger
LANGUAGE plpgsql
AS $sagalore$
DECLARE
    ev_type text;
    saga uuid;
    ver integer;
    audience uuid;
    member uuid;
BEGIN
    IF TG_TABLE_NAME = 'quests' THEN
        IF TG_OP = 'INSERT' THEN
            ev_type := 'saga.created';
        ELSIF TG_OP = 'UPDATE' THEN
            IF NEW.version IS NOT DISTINCT FROM OLD.version THEN
                RETURN NULL;
            END IF;
            IF NEW.active = false AND OLD.active = true THEN
                ev_type := 'saga.retired';
            ELSE
                ev_type := 'saga.updated';
            END IF;
        ELSE
            RETURN NULL;
        END IF;
        saga := NEW.id;
        ver := NEW.version;
    ELSIF TG_TABLE_NAME = 'progress' THEN
        IF TG_OP <> 'INSERT' OR NEW.status IS DISTINCT FROM 'completed' THEN
            RETURN NULL;
        END IF;
        ev_type := 'saga.completed';
        saga := NEW.quest_id;
        ver := NEW.quest_version;
    ELSIF TG_TABLE_NAME = 'characters' THEN
        IF TG_OP <> 'UPDATE' OR NEW.level IS NOT DISTINCT FROM OLD.level THEN
            RETURN NULL;
        END IF;
        ev_type := 'level.changed';
        audience := NEW.player_id;
    ELSE
        RETURN NULL;
    END IF;

    IF ev_type = 'level.changed' THEN
        PERFORM pg_notify(
            'sagalore_events',
            json_strip_nulls(json_build_object(
                'type', ev_type,
                'player_id', audience
            ))::text
        );
        RETURN NULL;
    END IF;

    FOR member IN
        SELECT gm.player_id
        FROM group_members gm
        JOIN quests q ON q.group_id = gm.group_id
        WHERE q.id = saga
    LOOP
        PERFORM pg_notify(
            'sagalore_events',
            json_strip_nulls(json_build_object(
                'type', ev_type,
                'saga_id', saga,
                'version', ver,
                'player_id', member
            ))::text
        );
    END LOOP;
    RETURN NULL;
END;
$sagalore$;

DROP TRIGGER IF EXISTS sagalore_quests_notify ON quests;
CREATE TRIGGER sagalore_quests_notify
    AFTER INSERT OR UPDATE OF version, active ON quests
    FOR EACH ROW EXECUTE FUNCTION sagalore_notify_event();

DROP TRIGGER IF EXISTS sagalore_progress_notify ON progress;
CREATE TRIGGER sagalore_progress_notify
    AFTER INSERT ON progress
    FOR EACH ROW EXECUTE FUNCTION sagalore_notify_event();

DROP TRIGGER IF EXISTS sagalore_level_notify ON characters;
CREATE TRIGGER sagalore_level_notify
    AFTER UPDATE OF level ON characters
    FOR EACH ROW EXECUTE FUNCTION sagalore_notify_event();
