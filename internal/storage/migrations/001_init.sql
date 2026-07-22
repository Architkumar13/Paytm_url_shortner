-- Sequence backing collision-free auto-generated codes. Started at a large
-- offset so the shortest code is a few characters (base62(1000000) = "4C92").
CREATE SEQUENCE IF NOT EXISTS link_code_seq START 1000000;

CREATE TABLE IF NOT EXISTS links (
    id             BIGSERIAL PRIMARY KEY,
    code           TEXT        NOT NULL UNIQUE,
    original_url   TEXT        NOT NULL,
    is_custom      BOOLEAN     NOT NULL DEFAULT FALSE,
    click_count    BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_access_at TIMESTAMPTZ
);

-- Enforces idempotent de-duplication for auto-generated links only: a given
-- URL maps to at most one non-custom code. Custom aliases are excluded so one
-- URL can still have multiple custom aliases.
CREATE UNIQUE INDEX IF NOT EXISTS links_original_url_noncustom
    ON links (original_url)
    WHERE is_custom = FALSE;

CREATE TABLE IF NOT EXISTS clicks (
    id         BIGSERIAL   PRIMARY KEY,
    link_id    BIGINT      NOT NULL REFERENCES links(id) ON DELETE CASCADE,
    clicked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    referer    TEXT        NOT NULL DEFAULT '',
    user_agent TEXT        NOT NULL DEFAULT '',
    ip         TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS clicks_link_id_idx ON clicks (link_id);
