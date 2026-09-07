-- credential_ref names an account's token file and is its stable identity
-- across config reloads, so upserts key on it.
CREATE UNIQUE INDEX idx_accounts_credential_ref ON accounts (credential_ref);
