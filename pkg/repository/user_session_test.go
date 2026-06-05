//go:build !e2e && !load && !rampup && !integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hatchet-dev/hatchet/pkg/repository/sqlcv1"
)

func createUserSessionRepository(pool *pgxpool.Pool) *userSessionRepository {
	logger := zerolog.Nop()
	shared := &sharedRepository{
		pool:    pool,
		ddlPool: pool,
		l:       &logger,
		queries: sqlcv1.New(),
	}
	return &userSessionRepository{
		sharedRepository: shared,
	}
}

func createTestUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	userId := uuid.New()
	_, err := pool.Exec(ctx(t), `
		INSERT INTO "User" ("id", "email", "emailVerified", "name", "createdAt", "updatedAt")
		VALUES ($1, $2, false, $3, NOW(), NOW())
	`, userId, userId.String()+"@test.com", "Test User")
	require.NoError(t, err)
	return userId
}

// TestCleanupUserSessions verifies the cleanup logic handles both conditions:
// 1. Expired sessions (expiresAt < NOW())
// 2. Unauthenticated sessions (userId IS NULL) older than 24 hours
func TestCleanupUserSessions(t *testing.T) {
	pool, cleanup := setupPostgresWithMigration(t)
	defer cleanup()

	testUser := createTestUser(t, pool)
	repo := createUserSessionRepository(pool)

	// Empty table returns no errors and empty slice
	deleted, err := repo.CleanupUserSessions(ctx(t))
	require.NoError(t, err)
	assert.Empty(t, deleted)

	// Define test cases covering all cleanup conditions
	testCases := []struct {
		name         string
		expiresAt    time.Time
		userId       *uuid.UUID
		ageHours     int
		shouldDelete bool
	}{
		// Condition 1: Expired sessions
		{
			name:         "expired-session-with-user",
			expiresAt:    time.Now().UTC().Add(-1 * time.Hour),
			userId:       &testUser,
			ageHours:     0,
			shouldDelete: true,
		},
		{
			name:         "just-expired-session",
			expiresAt:    time.Now().UTC().Add(-1 * time.Second),
			userId:       &testUser,
			ageHours:     0,
			shouldDelete: true,
		},

		// Condition 2: Unauthenticated and old sessions
		{
			name:         "unauthenticated-old-session",
			expiresAt:    time.Now().UTC().Add(48 * time.Hour),
			userId:       nil,
			ageHours:     25,
			shouldDelete: true,
		},

		// Cases that should NOT be deleted
		{
			name:         "valid-session-with-user",
			expiresAt:    time.Now().UTC().Add(24 * time.Hour),
			userId:       &testUser,
			ageHours:     0,
			shouldDelete: false,
		},
		{
			name:         "unauthenticated-recent-session",
			expiresAt:    time.Now().UTC().Add(48 * time.Hour),
			userId:       nil,
			ageHours:     1,
			shouldDelete: false,
		},
		{
			name:         "session-expires-in-1-second",
			expiresAt:    time.Now().UTC().Add(1 * time.Second),
			userId:       &testUser,
			ageHours:     0,
			shouldDelete: false,
		},
	}

	// Insert all test sessions
	sessionIds := make(map[string]uuid.UUID)
	for _, tc := range testCases {
		sessionId := uuid.New()
		sessionIds[tc.name] = sessionId

		var err error
		if tc.ageHours > 0 {
			_, err = pool.Exec(ctx(t), `
				INSERT INTO "UserSession" ("id", "expiresAt", "userId", "data", "createdAt", "updatedAt")
				VALUES ($1, $2 AT TIME ZONE 'UTC', $3, '{}', NOW() - INTERVAL '1 hour' * $4, NOW() - INTERVAL '1 hour' * $4)
			`, sessionId, tc.expiresAt, tc.userId, tc.ageHours)
		} else {
			_, err = pool.Exec(ctx(t), `
				INSERT INTO "UserSession" ("id", "expiresAt", "userId", "data", "createdAt", "updatedAt")
				VALUES ($1, $2 AT TIME ZONE 'UTC', $3, '{}', NOW(), NOW())
			`, sessionId, tc.expiresAt, tc.userId)
		}
		require.NoError(t, err, "failed to insert session: %s", tc.name)
	}

	// Run cleanup
	deleted, err = repo.CleanupUserSessions(ctx(t))
	require.NoError(t, err)

	// Verify return values contain deleted IDs
	deletedSet := make(map[uuid.UUID]bool)
	for _, id := range deleted {
		deletedSet[id] = true
	}

	// Verify each case
	for _, tc := range testCases {
		sessionId := sessionIds[tc.name]
		wasDeleted := deletedSet[sessionId]

		// Check return value
		if tc.shouldDelete {
			assert.True(t, wasDeleted, "session '%s' should be in deleted list", tc.name)
		} else {
			assert.False(t, wasDeleted, "session '%s' should NOT be in deleted list", tc.name)
		}

		// Verify DB state matches expectation
		_, err := repo.GetById(ctx(t), sessionId)
		if tc.shouldDelete {
			assert.Error(t, err, "session '%s' should be deleted from DB", tc.name)
		} else {
			assert.NoError(t, err, "session '%s' should exist in DB", tc.name)
		}
	}
}

// Helper functions

func ctx(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

