-- +goose Up

-- uuidv7() shim: provides time-ordered UUIDs on Postgres < 18.
-- On Postgres 18+, the built-in uuidv7() takes precedence at runtime.
-- Replace with pg_uuidv7 extension or the Postgres 18 native function in production.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION uuidv7() RETURNS uuid LANGUAGE sql AS 'SELECT gen_random_uuid()';
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS uuidv7();
