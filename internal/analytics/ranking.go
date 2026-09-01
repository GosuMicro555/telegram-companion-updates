package analytics

import (
	"encoding/binary"
	"hash/fnv"
	"math/bits"
	"sort"
	"strings"

	analytictext "telegram-companion/internal/analytics/text"
)

type Document struct {
	Text     string
	Topic    string
	SourceID string
}

type ScoreComponents struct {
	Frequency       float64
	SourceDiversity float64
	PhraseLength    float64
	ProfileMatch    float64
	MeaningSignal   float64
}

func (s ScoreComponents) Total() float64 {
	return s.Frequency + s.SourceDiversity + s.PhraseLength + s.ProfileMatch + s.MeaningSignal
}

type Candidate struct {
	NormalizedValue string
	DisplayValue    string
	Kind            string
	Frequency       int
	SourceDiversity int
	Score           float64
	Components      ScoreComponents
}

type Selection struct {
	GeneralPhrases    []Candidate
	GeneralWords      []Candidate
	ProfileCandidates []Candidate
}

type aggregate struct {
	normalized  string
	kind        string
	frequency   int
	sources     map[string]struct{}
	surfaces    map[string]int
	tokens      []string
	fingerprint uint64
}

func Rank(documents []Document, profile *Profile) []Candidate {
	return RankWithStemmer(documents, profile, analytictext.RussianStemmer{})
}

func RankWithStemmer(documents []Document, profile *Profile, stemmer analytictext.Stemmer) []Candidate {
	documents = append([]Document(nil), documents...)
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].Topic != documents[j].Topic {
			return documents[i].Topic < documents[j].Topic
		}
		if documents[i].SourceID != documents[j].SourceID {
			return documents[i].SourceID < documents[j].SourceID
		}
		return documents[i].Text < documents[j].Text
	})
	clusters := make([]*aggregate, 0)
	byExact := make(map[string]*aggregate)
	for index, document := range documents {
		source := document.SourceID
		if source == "" {
			source = stableSource(index)
		}
		for _, surface := range analytictext.Generate(document.Text, 1, 4) {
			tokens := strings.Fields(surface)
			if !eligible(tokens) {
				continue
			}
			normalized := strings.Join(analytictext.StemTokens(strings.Fields(analytictext.Normalize(surface)), stemmer), " ")
			kind := "phrase"
			if len(tokens) == 1 {
				kind = "word"
			}
			key := kind + "\x00" + normalized
			item := byExact[key]
			if item == nil {
				fingerprint := simHash(strings.Fields(normalized))
				item = nearCluster(clusters, kind, len(tokens), fingerprint)
				if item == nil {
					item = &aggregate{normalized: normalized, kind: kind, sources: map[string]struct{}{}, surfaces: map[string]int{}, tokens: tokens, fingerprint: fingerprint}
					clusters = append(clusters, item)
				}
				byExact[key] = item
			}
			item.frequency++
			item.sources[source] = struct{}{}
			item.surfaces[surface]++
		}
	}

	result := make([]Candidate, 0, len(clusters))
	for _, item := range clusters {
		profileScore := 0.0
		if profile != nil {
			profileScore = profile.Score(item.tokens)
		}
		components := ScoreComponents{
			Frequency:       float64(item.frequency) * 10,
			SourceDiversity: float64(len(item.sources)) * 3,
			PhraseLength:    float64(len(item.tokens)),
			ProfileMatch:    profileScore * 10,
			MeaningSignal:   meaningSignal(item.tokens),
		}
		result = append(result, Candidate{
			NormalizedValue: item.normalized, DisplayValue: bestSurface(item.surfaces), Kind: item.kind,
			Frequency: item.frequency, SourceDiversity: len(item.sources), Score: components.Total(), Components: components,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].NormalizedValue < result[j].NormalizedValue
	})
	return result
}

func Select(ranked []Candidate) Selection {
	var result Selection
	for _, candidate := range ranked {
		switch candidate.Kind {
		case "phrase":
			if len(result.GeneralPhrases) < 100 {
				result.GeneralPhrases = append(result.GeneralPhrases, candidate)
			}
		case "word":
			if len(result.GeneralWords) < 30 {
				result.GeneralWords = append(result.GeneralWords, candidate)
			}
		}
		if candidate.Components.ProfileMatch > 0 && len(result.ProfileCandidates) < 100 {
			result.ProfileCandidates = append(result.ProfileCandidates, candidate)
		}
	}
	return result
}

var boundaryStopwords = map[string]struct{}{
	"и": {}, "а": {}, "но": {}, "в": {}, "во": {}, "на": {}, "с": {}, "со": {}, "к": {}, "у": {}, "о": {}, "об": {}, "от": {}, "до": {}, "для": {}, "по": {}, "за": {}, "из": {}, "мне": {}, "я": {}, "мы": {}, "это": {},
}

func eligible(tokens []string) bool {
	if len(tokens) == 0 || len([]rune(tokens[0])) < 2 {
		return false
	}
	if len(tokens) > 1 {
		_, startsStop := boundaryStopwords[tokens[0]]
		_, endsStop := boundaryStopwords[tokens[len(tokens)-1]]
		return !startsStop && !endsStop
	}
	return true
}

func meaningSignal(tokens []string) float64 {
	for _, token := range tokens {
		if token == "не" || token == "нет" {
			return 2
		}
	}
	return 0
}

func nearCluster(clusters []*aggregate, kind string, tokenCount int, fingerprint uint64) *aggregate {
	if tokenCount < 3 {
		return nil
	}
	for _, item := range clusters {
		if item.kind == kind && len(item.tokens) == tokenCount && bits.OnesCount64(item.fingerprint^fingerprint) <= 8 {
			return item
		}
	}
	return nil
}

func simHash(tokens []string) uint64 {
	var weights [64]int
	for _, token := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		value := h.Sum64()
		for bit := range weights {
			if value&(uint64(1)<<bit) != 0 {
				weights[bit]++
			} else {
				weights[bit]--
			}
		}
	}
	var result uint64
	for bit, weight := range weights {
		if weight >= 0 {
			result |= uint64(1) << bit
		}
	}
	return result
}

func bestSurface(surfaces map[string]int) string {
	keys := make([]string, 0, len(surfaces))
	for surface := range surfaces {
		keys = append(keys, surface)
	}
	sort.Strings(keys)
	best := ""
	for _, surface := range keys {
		if best == "" || surfaces[surface] > surfaces[best] {
			best = surface
		}
	}
	return best
}

func stableSource(index int) string {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], uint64(index))
	return string(value[:])
}
