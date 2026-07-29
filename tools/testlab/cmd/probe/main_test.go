package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/hostfacts"
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

func resumeEnvelopeFixture() report.Report {
	facts := hostfacts.Facts{
		OSFamily: "windows", Arch: "amd64", OSVersion: "Windows", CPUModel: "CPU",
		CPULogicalCores: 28, MemoryTotalBytes: 64 << 30, SQLiteVersion: "3.50.0",
		SQLiteLibrary: "modernc.org/sqlite", GoVersion: "go1.26.5", GoMaxProcs: 2,
		Storage: hostfacts.Storage{Medium: hostfacts.MediumSSD, Model: "SSD", BusType: "NVMe", VolumeID: "volume-hash", PhysicalDiskNumbers: []int{3}},
	}
	return report.Report{
		SchemaVersion: 2, Scenario: "perf", ScenarioAlias: "query-reference", StorageClass: "ssd",
		Tier: "reference", Scale: corpus.ReferenceScale, Environment: &facts,
		Corpus: &report.CorpusFacts{Scale: corpus.ReferenceScale, SourceCount: corpus.ReferenceSourceCount,
			RelationsPerWork: corpus.ReferenceRelationsPerWork, SourceAliases: corpus.ReferenceSourceAliases()},
	}
}

func TestValidateQueryPerfResumeEnvelopeRejectsEnvironmentAndCorpusDrift(t *testing.T) {
	current := resumeEnvelopeFixture()
	recorded := resumeEnvelopeFixture()
	recorded.Transport = "loopback-http"
	if err := validateQueryPerfResumeEnvelope(recorded, current); err != nil {
		t.Fatalf("相同运行事实被拒绝: %v", err)
	}

	drifted := resumeEnvelopeFixture()
	drifted.Environment.GoMaxProcs = 4
	drifted.Corpus.SourceAliases[0] = "different-source"
	err := validateQueryPerfResumeEnvelope(recorded, drifted)
	if err == nil || !strings.Contains(err.Error(), "environment.goMaxProcs") || !strings.Contains(err.Error(), "corpus") {
		t.Fatalf("漂移错误未列出字段名: %v", err)
	}
	if strings.Contains(err.Error(), "different-source") || strings.Contains(err.Error(), "GoMaxProcs:4") {
		t.Fatalf("漂移错误泄露了字段值: %v", err)
	}
}

func TestQueryPerfPublicationFingerprintBindsBothIDs(t *testing.T) {
	base := corpus.Manifest{QueryPublicationID: "qpub-a", CatalogRevisionID: "crev-a"}
	fingerprint := queryPerfPublicationFingerprint(base)
	if fingerprint == "" || len(fingerprint) != 64 {
		t.Fatalf("fingerprint=%q", fingerprint)
	}
	changedPublication := base
	changedPublication.QueryPublicationID = "qpub-b"
	changedRevision := base
	changedRevision.CatalogRevisionID = "crev-b"
	if fingerprint == queryPerfPublicationFingerprint(changedPublication) || fingerprint == queryPerfPublicationFingerprint(changedRevision) {
		t.Fatal("任一 publication 身份字段变化都必须改变指纹")
	}
}

func TestFinalizeEnvironmentGateReplacesExistingFinding(t *testing.T) {
	rep := resumeEnvelopeFixture()
	rep.Latencies = []report.LatencySample{{PlannedRuns: 1}}
	rep.Add("environment/gate-required-facts-complete", true, "")
	finalizeEnvironmentGate(&rep)
	count := 0
	for _, finding := range rep.Findings {
		if finding.Name == "environment/gate-required-facts-complete" {
			count++
		}
	}
	if count != 1 || rep.FailureCount != 0 {
		t.Fatalf("environment gate 未保持单一终态: findings=%+v failures=%d", rep.Findings, rep.FailureCount)
	}
}
