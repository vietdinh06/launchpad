package main

import (
    "crypto/sha256"
    "crypto/subtle"
    "net/http"
    "strings"
)
func requireDeployToken( expectedToken string, next http.HandlerFunc,
) http.HandlerFunc {
    return func(
        w http.ResponseWriter,
        r *http.Request,
    ) {

		authorization := r.Header.Get("Authorization")

		const prefix = "Bearer "

		if !strings.HasPrefix(authorization, prefix) {
			http.Error(
				w,
				"Invalid or missing authorization",
				http.StatusUnauthorized,
			)
			return
		}

		receivedToken := strings.TrimPrefix(
			authorization,
			prefix,
		)

 		expectedHash := sha256.Sum256(
            []byte(expectedToken),
        )

        receivedHash := sha256.Sum256(
            []byte(receivedToken),
        )

		if subtle.ConstantTimeCompare(expectedHash[:], receivedHash[:],) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}