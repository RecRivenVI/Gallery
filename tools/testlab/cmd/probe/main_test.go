package main

import (
	"slices"
	"testing"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

func TestRecordCorpusFactsCarriesReferenceIdentity(t *testing.T) {
	manifest := corpus.Manifest{
		Scale: corpus.ReferenceScale, Sources: corpus.ReferenceSourceCount,
		SourceAliases: corpus.ReferenceSourceAliases(), RelationsPerWork: corpus.ReferenceRelationsPerWork,
		SourceBeginDurationsMs: make([]int64, corpus.ReferenceSourceCount),
	}
	rep := &report.Report{}
	recordCorpusFacts(rep, manifest)
	if rep.Corpus == nil {
		t.Fatal("report.Corpus 为空")
	}
	if rep.Corpus.RelationsPerWork != corpus.ReferenceRelationsPerWork {
		t.Fatalf("RelationsPerWork=%d want=%d", rep.Corpus.RelationsPerWork, corpus.ReferenceRelationsPerWork)
	}
	if !slices.Equal(rep.Corpus.SourceAliases, corpus.ReferenceSourceAliases()) {
		t.Fatalf("SourceAliases=%v", rep.Corpus.SourceAliases)
	}
	manifest.SourceAliases[0] = "mutated-after-record"
	if !corpus.HasReferenceSourceAliases(rep.Corpus.SourceAliases) {
		t.Fatal("报告必须持有 manifest 来源代号的独立副本")
	}
}
