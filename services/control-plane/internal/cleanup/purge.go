package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

type rootKind string

const (
	projectRoot      rootKind = "project"
	conversationRoot rootKind = "conversation"
)

type purgeRun struct {
	id        uuid.UUID
	provider  *string
	ref       *string
	candidate *agentexec.SessionID
}

type purgeRoot struct {
	kind      rootKind
	id        uuid.UUID
	projectID uuid.UUID
	prefixes  []string
	keys      []string
	runs      []purgeRun
}

type purgeCatalog interface {
	roots(context.Context) ([]purgeRoot, error)
	hardDelete(context.Context, purgeRoot) error
}

type PurgeSummary struct {
	Candidates int      `json:"candidates"`
	Purged     []string `json:"purged"`
}

type MaintenanceSummary struct {
	DryRun  bool          `json:"dry_run"`
	Orphans OrphanSummary `json:"orphans"`
	Purge   PurgeSummary  `json:"purge"`
}

type Purger struct {
	catalog         purgeCatalog
	objects         objectstore.Store
	binding         sandbox.Binding
	removeWorkspace func(context.Context, uuid.UUID) error
	workspacePaths  func(uuid.UUID) agentexec.Paths
}

func NewPurger(pool *pgxpool.Pool, objects objectstore.Store, binding sandbox.Binding, workspaceRoot string) *Purger {
	removal := &workspaceRemoval{root: workspaceRoot}
	return &Purger{catalog: &postgresCatalog{pool: pool}, objects: objects, binding: binding, removeWorkspace: removal.Remove, workspacePaths: removal.Paths}
}

func RunMaintenance(ctx context.Context, purger *Purger, scanner *OrphanScanner, apply bool) (MaintenanceSummary, error) {
	summary := MaintenanceSummary{DryRun: !apply}
	preflight, err := purger.Purge(ctx, false)
	summary.Purge = preflight
	if err != nil {
		return summary, err
	}
	summary.Orphans, err = scanner.Scan(ctx, apply)
	if err != nil || !apply {
		return summary, err
	}
	summary.Purge, err = purger.Purge(ctx, true)
	return summary, err
}

func (p *Purger) Purge(ctx context.Context, apply bool) (PurgeSummary, error) {
	roots, err := p.catalog.roots(ctx)
	if err != nil {
		return PurgeSummary{}, err
	}
	summary := PurgeSummary{Candidates: len(roots), Purged: []string{}}
	for _, root := range roots {
		if err := p.validateRoot(root); err != nil {
			return summary, err
		}
		if !apply {
			continue
		}
		if err := p.purgeRoot(ctx, root); err != nil {
			return summary, fmt.Errorf("purge %s %s: %w", root.kind, root.id, err)
		}
		summary.Purged = append(summary.Purged, string(root.kind)+":"+root.id.String())
	}
	return summary, nil
}

func (p *Purger) validateRoot(root purgeRoot) error {
	if root.id == uuid.Nil || root.projectID == uuid.Nil || (root.kind != projectRoot && root.kind != conversationRoot) {
		return errors.New("invalid purge root")
	}
	artifactBase := "projects/" + root.projectID.String() + "/artifacts/"
	for _, prefix := range root.prefixes {
		id := strings.TrimSuffix(strings.TrimPrefix(prefix, artifactBase), "/")
		if !strings.HasPrefix(prefix, artifactBase) || uuid.Validate(id) != nil || prefix != artifactBase+id+"/" {
			return fmt.Errorf("unsafe artifact prefix %q", prefix)
		}
	}
	inputBase := "projects/" + root.projectID.String() + "/inputs/"
	for _, key := range root.keys {
		rest, owned := strings.CutPrefix(key, inputBase)
		inputID, name, separated := strings.Cut(rest, "/")
		if root.kind != projectRoot || !owned || !separated || uuid.Validate(inputID) != nil || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("unsafe input object key %q", key)
		}
	}
	for _, run := range root.runs {
		if run.provider != nil && *run.provider != string(p.binding.ID) {
			return fmt.Errorf("run %s recorded provider %q does not match configured provider %q; run SANDBOX_PROVIDER=%s make purge-deleted before changing provider, or reset the data", run.id, *run.provider, p.binding.ID, *run.provider)
		}
		if run.ref != nil && run.provider == nil {
			return fmt.Errorf("run %s has sandbox ref without recorded provider", run.id)
		}
		if run.candidate != nil && run.ref == nil {
			return fmt.Errorf("run %s has a candidate SDK session without a recoverable sandbox ref", run.id)
		}
	}
	return nil
}

func (p *Purger) purgeRoot(ctx context.Context, root purgeRoot) error {
	if err := p.validateRoot(root); err != nil {
		return err
	}
	for _, prefix := range root.prefixes {
		if err := p.objects.DeletePrefix(ctx, prefix); err != nil {
			return err
		}
	}
	for _, key := range root.keys {
		if err := p.objects.Delete(ctx, key); err != nil {
			return err
		}
	}
	for _, run := range root.runs {
		if err := p.purgeRun(ctx, run); err != nil {
			return err
		}
	}
	return p.catalog.hardDelete(ctx, root)
}

func (p *Purger) purgeRun(ctx context.Context, run purgeRun) error {
	if run.ref == nil {
		return p.removeWorkspace(ctx, run.id)
	}
	var paths agentexec.Paths
	if p.workspacePaths != nil {
		paths = p.workspacePaths(run.id)
	}
	lease, err := p.binding.Provider.Recover(ctx, sandbox.RecoverRequest{RunID: run.id, Ref: *run.ref, Paths: paths})
	if errors.Is(err, sandbox.ErrNotFound) {
		return p.removeWorkspace(ctx, run.id)
	}
	if err != nil {
		return err
	}
	if run.candidate != nil {
		if err := ignoreAbsent(lease.Runtime().DeleteSession(ctx, *run.candidate)); err != nil {
			return err
		}
	}
	if err := p.removeWorkspace(ctx, run.id); err != nil {
		return err
	}
	if err := ignoreAbsent(lease.Runtime().DeleteExecution(ctx, run.id)); err != nil {
		return err
	}
	return ignoreAbsent(lease.Release(ctx))
}

func ignoreAbsent(err error) error {
	if errors.Is(err, sandbox.ErrNotFound) || errors.Is(err, agentexec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type workspaceRemoval struct{ root string }

func (r *workspaceRemoval) Paths(id uuid.UUID) agentexec.Paths {
	return workspaces.NewMaterializer(r.root, nil).Paths(id)
}

func (r *workspaceRemoval) Remove(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil || !filepath.IsAbs(r.root) {
		return errors.New("invalid workspace identity or root")
	}
	root := filepath.Clean(r.root)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect workspace root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("workspace root must be a directory, not a symlink")
	}
	runRoot := filepath.Join(root, id.String())
	if filepath.Dir(runRoot) != root {
		return errors.New("unsafe run workspace path")
	}
	info, err = os.Lstat(runRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect run workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("run workspace must be a directory, not a symlink")
	}
	if err := filepath.WalkDir(runRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0770)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("unseal run workspace: %w", err)
	}
	if err := os.RemoveAll(runRoot); err != nil {
		return fmt.Errorf("remove run workspace: %w", err)
	}
	return nil
}

type postgresCatalog struct{ pool *pgxpool.Pool }

func (c *postgresCatalog) roots(ctx context.Context) ([]purgeRoot, error) {
	type candidate struct {
		kind      rootKind
		id        uuid.UUID
		projectID uuid.UUID
	}
	rows, err := c.pool.Query(ctx, `
		SELECT 'project',p.id,p.id,p.deleted_at FROM projects p
		WHERE p.deleted_at IS NOT NULL AND NOT EXISTS(
			SELECT 1 FROM runs r JOIN conversations c ON c.id=r.conversation_id
			WHERE c.project_id=p.id AND (r.status IN ('queued','running') OR r.finalized_at IS NULL))
		UNION ALL
		SELECT 'conversation',c.id,c.project_id,c.deleted_at FROM conversations c JOIN projects p ON p.id=c.project_id
		WHERE c.deleted_at IS NOT NULL AND p.deleted_at IS NULL AND NOT EXISTS(
			SELECT 1 FROM runs r WHERE r.conversation_id=c.id AND (r.status IN ('queued','running') OR r.finalized_at IS NULL))
		ORDER BY 4,2`)
	if err != nil {
		return nil, fmt.Errorf("list purge roots: %w", err)
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		var ignored any
		if err := rows.Scan(&item.kind, &item.id, &item.projectID, &ignored); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan purge root: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list purge roots: %w", err)
	}
	rows.Close()
	result := make([]purgeRoot, 0, len(candidates))
	for _, item := range candidates {
		root := purgeRoot{kind: item.kind, id: item.id, projectID: item.projectID}
		if err := c.loadRoot(ctx, &root); err != nil {
			return nil, err
		}
		result = append(result, root)
	}
	return result, nil
}

func (c *postgresCatalog) loadRoot(ctx context.Context, root *purgeRoot) error {
	condition, id := "c.project_id=$1", root.id
	if root.kind == conversationRoot {
		condition, id = "c.id=$1", root.id
	}
	runRows, err := c.pool.Query(ctx, `SELECT r.id,r.sandbox_provider,r.sandbox_ref,r.candidate_sdk_session_id FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE `+condition+` ORDER BY r.created_at,r.id`, id)
	if err != nil {
		return fmt.Errorf("list purge runs: %w", err)
	}
	for runRows.Next() {
		var run purgeRun
		var candidate *string
		if err := runRows.Scan(&run.id, &run.provider, &run.ref, &candidate); err != nil {
			runRows.Close()
			return fmt.Errorf("scan purge run: %w", err)
		}
		if candidate != nil && strings.TrimSpace(*candidate) != "" {
			session := agentexec.SessionID(*candidate)
			run.candidate = &session
		}
		root.runs = append(root.runs, run)
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return fmt.Errorf("list purge runs: %w", err)
	}
	runRows.Close()
	prefixRows, err := c.pool.Query(ctx, `SELECT a.object_prefix FROM artifacts a JOIN runs r ON r.id=a.run_id JOIN conversations c ON c.id=r.conversation_id WHERE `+condition+` ORDER BY a.object_prefix`, id)
	if err != nil {
		return fmt.Errorf("list purge artifacts: %w", err)
	}
	for prefixRows.Next() {
		var prefix string
		if err := prefixRows.Scan(&prefix); err != nil {
			prefixRows.Close()
			return err
		}
		root.prefixes = append(root.prefixes, prefix)
	}
	if err := prefixRows.Err(); err != nil {
		prefixRows.Close()
		return err
	}
	prefixRows.Close()
	if root.kind == projectRoot {
		keyRows, err := c.pool.Query(ctx, `SELECT object_key FROM input_files WHERE project_id=$1 ORDER BY object_key`, root.id)
		if err != nil {
			return fmt.Errorf("list purge inputs: %w", err)
		}
		for keyRows.Next() {
			var key string
			if err := keyRows.Scan(&key); err != nil {
				keyRows.Close()
				return err
			}
			root.keys = append(root.keys, key)
		}
		if err := keyRows.Err(); err != nil {
			keyRows.Close()
			return err
		}
		keyRows.Close()
	}
	return nil
}

func (c *postgresCatalog) hardDelete(ctx context.Context, root purgeRoot) (err error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin hard delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if root.kind == projectRoot {
		if err := lockPurgeProject(ctx, tx, root.id); err != nil {
			return err
		}
		statements := []string{
			`UPDATE messages SET run_id=NULL WHERE conversation_id IN (SELECT id FROM conversations WHERE project_id=$1)`,
			`DELETE FROM artifacts WHERE run_id IN (SELECT r.id FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE c.project_id=$1)`,
			`DELETE FROM run_events WHERE run_id IN (SELECT r.id FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE c.project_id=$1)`,
			`DELETE FROM runs WHERE conversation_id IN (SELECT id FROM conversations WHERE project_id=$1)`,
			`DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE project_id=$1)`,
			`DELETE FROM conversations WHERE project_id=$1`,
			`DELETE FROM input_files WHERE project_id=$1`,
			`DELETE FROM projects WHERE id=$1`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement, root.id); err != nil {
				return fmt.Errorf("hard delete project: %w", err)
			}
		}
	} else {
		if err := lockPurgeConversation(ctx, tx, root.id); err != nil {
			return err
		}
		statements := []string{
			`UPDATE messages SET run_id=NULL WHERE conversation_id=$1`,
			`DELETE FROM artifacts WHERE run_id IN (SELECT id FROM runs WHERE conversation_id=$1)`,
			`DELETE FROM run_events WHERE run_id IN (SELECT id FROM runs WHERE conversation_id=$1)`,
			`DELETE FROM runs WHERE conversation_id=$1`,
			`DELETE FROM messages WHERE conversation_id=$1`,
			`DELETE FROM conversations WHERE id=$1`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement, root.id); err != nil {
				return fmt.Errorf("hard delete conversation: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit hard delete: %w", err)
	}
	return nil
}

func lockPurgeProject(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var deleted, protected bool
	if err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE`, id).Scan(&deleted); err != nil {
		return fmt.Errorf("lock purge project: %w", err)
	}
	if !deleted {
		return errors.New("project is no longer logically deleted")
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE c.project_id=$1 AND (r.status IN ('queued','running') OR r.finalized_at IS NULL))`, id).Scan(&protected); err != nil {
		return err
	}
	if protected {
		return errors.New("project gained a protected run")
	}
	return nil
}

func lockPurgeConversation(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var deleted, parentDeleted, protected bool
	if err := tx.QueryRow(ctx, `SELECT c.deleted_at IS NOT NULL,p.deleted_at IS NOT NULL FROM conversations c JOIN projects p ON p.id=c.project_id WHERE c.id=$1 FOR UPDATE OF p,c`, id).Scan(&deleted, &parentDeleted); err != nil {
		return fmt.Errorf("lock purge conversation: %w", err)
	}
	if !deleted || parentDeleted {
		return errors.New("conversation is no longer an independent purge root")
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE conversation_id=$1 AND (status IN ('queued','running') OR finalized_at IS NULL))`, id).Scan(&protected); err != nil {
		return err
	}
	if protected {
		return errors.New("conversation gained a protected run")
	}
	return nil
}
