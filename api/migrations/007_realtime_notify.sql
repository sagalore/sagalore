-- Push channel for companion WebSockets.
-- Any API instance can LISTEN on sagalore_events. NOTIFY runs at commit,
-- so a player connected to another replica still hears the change.
-- Image-only updates do not mention version or active, so they do not fire.
-- A new write path should bump quests.version (UpdateQuest already does) or
-- SELECT pg_notify('sagalore_events', '{"type":"saga.updated","saga_id":"…","version":N}').

CREATE OR REPLACE FUNCTION sagalore_notify_event() RETURNS trigger
LANGUAGE plpgsql
AS $sagalore$
DECLARE
    ev_type text;
    saga uuid;
    ver integer;
    audience uuid;
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

    PERFORM pg_notify(
        'sagalore_events',
        json_strip_nulls(json_build_object(
            'type', ev_type,
            'saga_id', saga,
            'version', ver,
            'player_id', audience
        ))::text
    );
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
