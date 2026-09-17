package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/auth"
)

/*
Role bindings decide who may do what, so the administration of them is the one
surface where a mistake can lock everybody out of fixing the mistake.

The guard here is deliberately in the store rather than in a handler: a check
made outside the transaction that then deletes is a race, and the thing being
raced for is the ability to administer the deployment at all.
*/

// RoleBindings lists every binding with its matchers, ordered by name.
//
// Matchers are included where the diagnostics listing omits them: a scope is
// half of what a binding means, and an administration API that showed the role
// without it would be describing a different binding than the one in force.
func (store *Store) RoleBindings(ctx context.Context) ([]auth.RoleBinding, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT binding.name, binding.subject_kind, COALESCE(binding.user_id, 0),
		       COALESCE(binding.subject_issuer, ''), COALESCE(binding.subject_group, ''), binding.role,
		       COALESCE(matcher.label_name, ''), COALESCE(matcher.operator, ''), COALESCE(matcher.value, '')
		FROM role_bindings AS binding
		LEFT JOIN role_binding_matchers AS matcher ON matcher.role_binding_id = binding.id
		ORDER BY binding.name, matcher.ordinal
	`)
	if err != nil {
		return nil, fmt.Errorf("list role bindings: %w", err)
	}
	defer rows.Close()
	bindings := []auth.RoleBinding{}
	for rows.Next() {
		var binding auth.RoleBinding
		var name, operator, value string
		if err := rows.Scan(
			&binding.Name, &binding.SubjectKind, &binding.UserID,
			&binding.SubjectIssuer, &binding.SubjectGroup, &binding.Role,
			&name, &operator, &value,
		); err != nil {
			return nil, fmt.Errorf("scan role binding: %w", err)
		}
		// The join produces one row per matcher, so consecutive rows with the
		// same name are one binding.
		if len(bindings) > 0 && bindings[len(bindings)-1].Name == binding.Name {
			binding = bindings[len(bindings)-1]
			bindings = bindings[:len(bindings)-1]
		}
		if name != "" {
			binding.Matchers = append(binding.Matchers, auth.LabelMatcher{
				Name: name, Operator: operator, Value: value,
			})
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role bindings: %w", err)
	}
	return bindings, nil
}

// guardLastAdministrator fails when the binding named is the only administrator
// one left and the change would stop it being that.
//
// Called inside the caller's transaction so the count and the write cannot be
// separated by somebody else's delete.
func guardLastAdministrator(ctx context.Context, tx pgx.Tx, name string, stillAdministrator bool) error {
	var administrators int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM role_bindings WHERE role = $1", auth.RoleAdministrator,
	).Scan(&administrators); err != nil {
		return fmt.Errorf("count administrator bindings: %w", err)
	}
	if administrators == 0 {
		// A deployment with none never had one to lose; open mode is the usual
		// case, and the first administrator has to be creatable.
		return nil
	}
	var isAdministrator bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM role_bindings WHERE name = $1 AND role = $2)",
		name, auth.RoleAdministrator,
	).Scan(&isAdministrator); err != nil {
		return fmt.Errorf("read role binding %s: %w", name, err)
	}
	if isAdministrator && administrators == 1 && !stillAdministrator {
		return auth.ErrLastAdministrator
	}
	return nil
}
