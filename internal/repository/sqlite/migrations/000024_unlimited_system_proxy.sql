-- +goose Up
DROP TRIGGER accounts_proxy_assignment_update;
DROP TRIGGER accounts_proxy_assignment_insert;

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_insert
BEFORE INSERT ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_update
BEFORE UPDATE OF proxy_mode,proxy_profile_id ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id AND id<>NEW.id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER accounts_proxy_assignment_update;
DROP TRIGGER accounts_proxy_assignment_insert;

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_insert
BEFORE INSERT ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='global' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='global') >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_update
BEFORE UPDATE OF proxy_mode,proxy_profile_id ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='global' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='global' AND id<>NEW.id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id AND id<>NEW.id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd
