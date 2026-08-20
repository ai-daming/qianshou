ALTER TABLE brief_versions ADD COLUMN issue_updated_at TEXT;
ALTER TABLE brief_versions ADD COLUMN issue_body_sha256 TEXT CHECK (issue_body_sha256 IS NULL OR length(issue_body_sha256) = 64);
