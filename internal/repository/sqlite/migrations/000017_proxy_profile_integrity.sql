-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER proxy_profiles_assignment_delete_guard
BEFORE DELETE ON proxy_profiles
WHEN EXISTS(SELECT 1 FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=OLD.id)
BEGIN
  SELECT RAISE(ABORT, 'proxy profile is assigned to accounts');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER proxy_profiles_assignment_delete_guard;
