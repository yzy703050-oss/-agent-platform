package middleware

import (
	"github.com/gin-gonic/gin"
	"strconv"
	"sync"
	"time"
)

// RateLimit bounds both attempts and memory. It is process-local for this single-instance MVP.
func RateLimit(max int, window time.Duration) gin.HandlerFunc {
	type entry struct {
		count   int
		expires time.Time
	}
	var mu sync.Mutex
	entries := map[string]entry{}
	return func(c *gin.Context) {
		now := time.Now()
		key := c.ClientIP()
		mu.Lock()
		e := entries[key]
		if !now.Before(e.expires) {
			e = entry{expires: now.Add(window)}
		}
		if len(entries) >= 4096 {
			for k, v := range entries {
				if !now.Before(v.expires) {
					delete(entries, k)
				}
			}
			if _, exists := entries[key]; !exists && len(entries) >= 4096 {
				mu.Unlock()
				Reject(c, 429, "rate_limited")
				return
			}
		}
		e.count++
		entries[key] = e
		limited := e.count > max
		mu.Unlock()
		if limited {
			c.Header("Retry-After", strconv.Itoa(int(time.Until(e.expires).Seconds())+1))
			Reject(c, 429, "rate_limited")
			return
		}
		c.Next()
	}
}
