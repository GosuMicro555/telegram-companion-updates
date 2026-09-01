package usecase

import "math/rand/v2"

type randomJoinDelaySource struct{}

func (randomJoinDelaySource) Minutes(min, max int) (int, error) {
	//nolint:gosec // G404: join scheduling jitter is not a secret or security boundary.
	return min + rand.IntN(max-min+1), nil
}
