
CREATE TABLE applications (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE releases (
    id UUID PRIMARY KEY,
    application_id UUID NOT NULL
        REFERENCES applications(id),
    version INTEGER NOT NULL,
    image_ref TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(application_id, version)
);

CREATE TABLE deployment_jobs (
    id UUID PRIMARY KEY,
    release_id UUID NOT NULL
        REFERENCES releases(id),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN (
            'pending',
            'running',
            'succeeded',
            'failed'
        )),
    container_id TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_jobs_status
    ON deployment_jobs(status);
