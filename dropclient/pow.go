package dropclient

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const maxPowIterations = 64_000_000

type powSolution struct {
	Checkpoints string `json:"checkpoints"`
}

func SolveChallenge(challenge challengeResult) (powSolution, error) {
	if challenge.K <= 0 {
		return powSolution{}, fmt.Errorf("invalid challenge k %d", challenge.K)
	}
	if challenge.G <= 0 {
		return powSolution{}, fmt.Errorf("invalid challenge g %d", challenge.G)
	}
	if challenge.K*challenge.G > maxPowIterations {
		return powSolution{}, fmt.Errorf("challenge iterations %d exceed max %d", challenge.K*challenge.G, maxPowIterations)
	}
	seed, err := base64.RawURLEncoding.DecodeString(challenge.Seed)
	if err != nil {
		return powSolution{}, fmt.Errorf("decode challenge seed: %w", err)
	}
	if len(seed) != sha256.Size {
		return powSolution{}, fmt.Errorf("challenge seed must decode to %d bytes, got %d", sha256.Size, len(seed))
	}
	current := sha256.Sum256(seed)
	checkpoints := make([]byte, 0, (challenge.K+1)*sha256.Size)
	checkpoints = append(checkpoints, current[:]...)
	for i := 0; i < challenge.K; i++ {
		for j := 0; j < challenge.G; j++ {
			current = sha256.Sum256(current[:])
		}
		checkpoints = append(checkpoints, current[:]...)
	}
	return powSolution{Checkpoints: base64.StdEncoding.EncodeToString(checkpoints)}, nil
}
