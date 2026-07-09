package dropclient

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestSolveChallenge(t *testing.T) {
	seed := make([]byte, sha256.Size)
	for i := range seed {
		seed[i] = byte(i)
	}
	challenge := challengeResult{
		ChallengeToken: "challenge-token",
		Seed:           base64.RawURLEncoding.EncodeToString(seed),
		K:              2,
		G:              3,
	}

	got, err := SolveChallenge(challenge)
	if err != nil {
		t.Fatalf("SolveChallenge returned error: %v", err)
	}

	current := sha256.Sum256(seed)
	checkpoints := append([]byte{}, current[:]...)
	for range challenge.K {
		for range challenge.G {
			current = sha256.Sum256(current[:])
		}
		checkpoints = append(checkpoints, current[:]...)
	}
	want := base64.StdEncoding.EncodeToString(checkpoints)
	if got.Checkpoints != want {
		t.Fatalf("checkpoints = %q, want %q", got.Checkpoints, want)
	}
}

func TestSolveChallengeRejectsLargeWork(t *testing.T) {
	seed := base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
	_, err := SolveChallenge(challengeResult{Seed: seed, K: 8000, G: 8001})
	if err == nil {
		t.Fatal("SolveChallenge returned nil error for oversized challenge")
	}
}
