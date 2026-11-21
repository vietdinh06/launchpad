CREATE INDEX IF NOT EXISTS idx_releases_application
ON releases(application_id, version DESC);

CREATE INDEX IF NOT EXISTS idx_jobs_release
ON deployment_jobs(release_id);
