// Package transfer plans and executes filesystem transfers.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/rituu/sshut/internal/filesystem"
)

// Decision is the action selected for a destination conflict.
type Decision uint8

const (
	DecisionOverwrite Decision = iota + 1
	DecisionSkip
	DecisionKeepBoth
	DecisionCancel
)

// String returns a stable human-readable decision name.
func (d Decision) String() string {
	switch d {
	case DecisionOverwrite:
		return "overwrite"
	case DecisionSkip:
		return "skip"
	case DecisionKeepBoth:
		return "keep both"
	case DecisionCancel:
		return "cancel"
	default:
		return "unknown"
	}
}

// Conflict describes one existing destination entry.
type Conflict struct {
	Source          filesystem.Entry
	Destination     filesystem.Entry
	SourcePath      string
	DestinationPath string
}

// Resolver asks the user what to do about a conflict. Implementations may
// apply one answer to all remaining conflicts in a request.
type Resolver interface {
	Resolve(context.Context, Conflict) (Decision, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(context.Context, Conflict) (Decision, error)

// Resolve calls the wrapped function.
func (f ResolverFunc) Resolve(ctx context.Context, conflict Conflict) (Decision, error) {
	return f(ctx, conflict)
}

// Action describes how a planned node interacts with its destination.
type Action uint8

const (
	ActionCreate Action = iota + 1
	ActionMerge
	ActionReplace
	ActionSkip
)

// String returns a stable human-readable action name.
func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionMerge:
		return "merge"
	case ActionReplace:
		return "replace"
	case ActionSkip:
		return "skip"
	default:
		return "unknown"
	}
}

// Node is one entry in a recursively expanded transfer plan.
type Node struct {
	SourcePath      string
	DestinationPath string
	Source          filesystem.Entry
	Action          Action
	Children        []*Node
}

// Plan is an immutable transfer tree ready for execution.
type Plan struct {
	Roots      []*Node
	TotalBytes int64
	TotalFiles int
}

// BuildPlan expands selected entries recursively without following symbolic
// links. Destination conflicts are resolved before any data is changed.
func BuildPlan(
	ctx context.Context,
	sourceFS filesystem.FS,
	destinationFS filesystem.FS,
	destinationDir string,
	selected []filesystem.Entry,
	resolver Resolver,
) (*Plan, error) {
	planner := planner{
		source:      sourceFS,
		destination: destinationFS,
		resolver:    resolver,
		plan:        &Plan{},
	}
	for _, selectedEntry := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := filesystem.ValidateName(selectedEntry.Name); err != nil {
			return nil, err
		}
		destinationPath := destinationFS.Join(destinationDir, selectedEntry.Name)
		node, err := planner.build(ctx, selectedEntry.Path, destinationPath)
		if err != nil {
			return nil, err
		}
		planner.plan.Roots = append(planner.plan.Roots, node)
	}
	return planner.plan, nil
}

type planner struct {
	source      filesystem.FS
	destination filesystem.FS
	resolver    Resolver
	plan        *Plan
}

func (p *planner) build(ctx context.Context, sourcePath, destinationPath string) (*Node, error) {
	source, err := p.source.Stat(ctx, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("stat source %q: %w", sourcePath, err)
	}
	if source.Kind == filesystem.KindOther {
		return nil, fmt.Errorf("unsupported special file: %s", sourcePath)
	}

	node := &Node{
		SourcePath:      source.Path,
		DestinationPath: destinationPath,
		Source:          source,
		Action:          ActionCreate,
	}

	destination, destinationErr := p.destination.Stat(ctx, destinationPath)
	destinationExists := destinationErr == nil
	if destinationErr != nil && !isNotExist(destinationErr) {
		return nil, fmt.Errorf("stat destination %q: %w", destinationPath, destinationErr)
	}

	if destinationExists {
		if source.IsDir() && destination.IsDir() {
			node.Action = ActionMerge
		} else {
			conflict := Conflict{
				Source:          source,
				Destination:     destination,
				SourcePath:      source.Path,
				DestinationPath: destinationPath,
			}
			decision, err := p.resolve(ctx, conflict)
			if err != nil {
				return nil, err
			}
			switch decision {
			case DecisionSkip:
				node.Action = ActionSkip
				return node, nil
			case DecisionCancel:
				return nil, context.Canceled
			case DecisionOverwrite:
				node.Action = ActionReplace
			case DecisionKeepBoth:
				destinationPath, err = p.keepBothPath(ctx, destinationPath)
				if err != nil {
					return nil, err
				}
				node.DestinationPath = destinationPath
			default:
				return nil, fmt.Errorf("invalid conflict decision %q", decision)
			}
		}
	}

	if source.IsDir() {
		entries, err := p.source.List(ctx, source.Path)
		if err != nil {
			return nil, fmt.Errorf("list source %q: %w", source.Path, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			child, err := p.build(ctx, entry.Path, p.destination.Join(destinationPath, entry.Name))
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		}
		return node, nil
	}

	p.plan.TotalFiles++
	if source.Kind == filesystem.KindFile {
		p.plan.TotalBytes += source.Size
	}
	return node, nil
}

func (p *planner) resolve(ctx context.Context, conflict Conflict) (Decision, error) {
	if p.resolver == nil {
		return DecisionCancel, fmt.Errorf("conflict at %q requires a resolver", conflict.DestinationPath)
	}
	decision, err := p.resolver.Resolve(ctx, conflict)
	if err != nil {
		return DecisionCancel, err
	}
	return decision, nil
}

func (p *planner) keepBothPath(ctx context.Context, existing string) (string, error) {
	dir := p.destination.Parent(existing)
	name := p.destination.Base(existing)
	stem, extension := splitName(name)
	for index := 1; index < 10_000; index++ {
		candidate := p.destination.Join(dir, fmt.Sprintf("%s (%d)%s", stem, index, extension))
		_, err := p.destination.Stat(ctx, candidate)
		if isNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("find keep-both destination: %w", err)
		}
	}
	return "", fmt.Errorf("could not find a keep-both name for %q", existing)
}

func splitName(name string) (stem, extension string) {
	extension = path.Ext(name)
	if extension == name {
		return "", ""
	}
	return strings.TrimSuffix(name, extension), extension
}

func isNotExist(err error) bool {
	return err != nil && errors.Is(err, fs.ErrNotExist)
}
