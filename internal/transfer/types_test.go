package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rituu/sshut/internal/filesystem"
	"github.com/rituu/sshut/internal/local"
)

func TestBuildPlanRecursivelyTotalsFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)

	writeTestFile(t, filepath.Join(sourceRoot, "project", "a.txt"), "abc")
	writeTestFile(t, filepath.Join(sourceRoot, "project", "nested", "b.txt"), "12345")
	entry, err := source.Stat(ctx, filepath.Join(sourceRoot, "project"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}

	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Roots) != 1 || plan.Roots[0].Action != ActionCreate {
		t.Fatalf("unexpected root nodes: %#v", plan.Roots)
	}
	if plan.TotalFiles != 2 || plan.TotalBytes != 8 {
		t.Fatalf("plan totals = %d files / %d bytes, want 2 / 8", plan.TotalFiles, plan.TotalBytes)
	}

	nested := findNode(plan.Roots[0], filepath.Join(sourceRoot, "project", "nested", "b.txt"))
	if nested == nil {
		t.Fatal("nested file missing from plan")
	}
	wantDestination := filepath.Join(destinationRoot, "project", "nested", "b.txt")
	if nested.DestinationPath != wantDestination {
		t.Fatalf("nested destination = %q, want %q", nested.DestinationPath, wantDestination)
	}
}

func TestBuildPlanMergesAndResolvesNestedConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)

	writeTestFile(t, filepath.Join(sourceRoot, "project", "report.txt"), "new")
	writeTestFile(t, filepath.Join(sourceRoot, "project", "docs", "manual.md"), "source")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "report.txt"), "old")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "docs", "existing.md"), "keep")
	writeTestFile(t, filepath.Join(destinationRoot, "project", "destination-only.txt"), "keep")

	entry, err := source.Stat(ctx, filepath.Join(sourceRoot, "project"))
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}
	var conflicts []Conflict
	resolver := ResolverFunc(func(_ context.Context, conflict Conflict) (Decision, error) {
		conflicts = append(conflicts, conflict)
		return DecisionOverwrite, nil
	})

	plan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, resolver)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Roots[0].Action != ActionMerge {
		t.Fatalf("root action = %v, want merge", plan.Roots[0].Action)
	}
	if len(conflicts) != 1 || filepath.Base(conflicts[0].DestinationPath) != "report.txt" {
		t.Fatalf("conflicts = %#v, want report.txt", conflicts)
	}
	fileNode := findNode(plan.Roots[0], filepath.Join(sourceRoot, "project", "report.txt"))
	if fileNode == nil || fileNode.Action != ActionReplace {
		t.Fatalf("report node = %#v, want replace", fileNode)
	}
}

func TestBuildPlanKeepBothAndSkip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	sourceFile := filepath.Join(sourceRoot, "report.txt")
	writeTestFile(t, sourceFile, "new")
	writeTestFile(t, filepath.Join(destinationRoot, "report.txt"), "old")
	writeTestFile(t, filepath.Join(destinationRoot, "report (1).txt"), "older")
	entry, err := source.Stat(ctx, sourceFile)
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}

	keepPlan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, ResolverFunc(
		func(context.Context, Conflict) (Decision, error) { return DecisionKeepBoth, nil },
	))
	if err != nil {
		t.Fatalf("BuildPlan keep both: %v", err)
	}
	if got := keepPlan.Roots[0].DestinationPath; got != filepath.Join(destinationRoot, "report (2).txt") {
		t.Fatalf("keep-both destination = %q", got)
	}

	skipPlan, err := BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, ResolverFunc(
		func(context.Context, Conflict) (Decision, error) { return DecisionSkip, nil },
	))
	if err != nil {
		t.Fatalf("BuildPlan skip: %v", err)
	}
	if skipPlan.Roots[0].Action != ActionSkip || skipPlan.TotalFiles != 0 {
		t.Fatalf("skip plan = %#v", skipPlan)
	}
}

func TestBuildPlanCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := local.NewAt(sourceRoot)
	destination := local.NewAt(destinationRoot)
	sourceFile := filepath.Join(sourceRoot, "file.txt")
	writeTestFile(t, sourceFile, "source")
	writeTestFile(t, filepath.Join(destinationRoot, "file.txt"), "destination")
	entry, err := source.Stat(ctx, sourceFile)
	if err != nil {
		t.Fatalf("Stat source: %v", err)
	}

	_, err = BuildPlan(ctx, source, destination, destinationRoot, []filesystem.Entry{entry}, ResolverFunc(
		func(context.Context, Conflict) (Decision, error) { return DecisionCancel, nil },
	))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildPlan error = %v, want context.Canceled", err)
	}
}

func writeTestFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func findNode(root *Node, sourcePath string) *Node {
	if root.SourcePath == sourcePath {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, sourcePath); found != nil {
			return found
		}
	}
	return nil
}
