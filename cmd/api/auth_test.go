package main

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestRequireDeployToken(t *testing.T) {
    const secret = "test-secret-token"

    tests := []struct {
        name     string
        header   string
        expected int
    }{
        {
            name:     "valid token",
            header:   "Bearer " + secret,
            expected: http.StatusOK,
        },
        {
            name:     "missing token",
            header:   "",
            expected: http.StatusUnauthorized,
        },
        {
            name:     "invalid token",
            header:   "Bearer wrong-token",
            expected: http.StatusUnauthorized,
        },
        {
            name:     "wrong authentication scheme",
            header:   "Basic " + secret,
            expected: http.StatusUnauthorized,
        },
    }

    handler := requireDeployToken(
        secret,
        func(
            w http.ResponseWriter,
            r *http.Request,
        ) {
            w.WriteHeader(http.StatusOK)
        },
    )

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := httptest.NewRequest(
                http.MethodPost,
                "/applications",
                nil,
            )

            if tt.header != "" {
                req.Header.Set(
                    "Authorization",
                    tt.header,
                )
            }

            recorder := httptest.NewRecorder()

            handler(recorder, req)

            if recorder.Code != tt.expected {
                t.Errorf(
                    "got %d, want %d",
                    recorder.Code,
                    tt.expected,
                )
            }
        })
    }
}
