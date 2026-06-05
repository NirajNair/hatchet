package cron

import (
	"context"
	"fmt"
	"time"
)

func (c *CronScheduler) runUserSessionsCleanup(ctx context.Context) func() {
	return func() {
		ctx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()

		deletedSessionIds, err := c.repoV1.UserSession().CleanupUserSessions(ctx)
		if err != nil {
			return fmt.Errorf("could not shutdown scheduler: %w", err)
		}

		c.l.Info().Ctx(ctx).Int("deleted_sessions", len(deletedSessionIds)).Msg("user sessions cleanup cron job completed")
	}
}
