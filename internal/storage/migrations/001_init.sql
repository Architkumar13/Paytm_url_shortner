-- Sequence backing collision-free auto-generated codes. Started at a large
-- offset so the shortest code is a few characters (base62(1000000) = "4C92").
CREATE SEQUENCE IF NOT EXISTS link_code_seq START 1000000;

CREATE TABLE IF NOT EXISTS links (
    id           BIGSERIAL PRIMARY KEY,
    code         TEXT        NOT NULL UNIQUE,
    original_url TEXT        NOT NULL,
    is_custom    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforces idempotent de-duplication for auto-generated links only: a given
-- URL maps to at most one non-custom code. Custom aliases are excluded so one
-- URL can still have multiple custom aliases.
CREATE UNIQUE INDEX IF NOT EXISTS links_original_url_noncustom
    ON links (original_url)
    WHERE is_custom = FALSE;
