ALTER TABLE releases
ADD COLUMN container_port INTEGER NOT NULL DEFAULT 80
CHECK (container_port BETWEEN 1 AND 65535);

CREATE INDEX IF NOT EXISTS idx_jobs_pending
ON deployment_jobs(created_at, id)
WHERE status = 'pending';
