package ilmari

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ============================================================================
// Phase 3: Operational Primitives - Scale, Restart, Rollback, CanI
// ============================================================================

// Scale changes the replica count of a deployment or statefulset.
// Resource format: "kind/name" (e.g., "deployment/myapp", "statefulset/mydb")
func (c *Context) Scale(resource string, replicas int) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Scale",
		attribute.String("resource", resource),
		attribute.Int("replicas", replicas))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid resource format %q, expected kind/name", resource)
		return err
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]
	ctx := context.Background()
	r := int32(replicas)

	switch kind {
	case "deployment":
		deploy, getErr := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
			return err
		}
		deploy.Spec.Replicas = &r
		_, err = c.Client.AppsV1().Deployments(c.Namespace).Update(ctx, deploy, metav1.UpdateOptions{})

	case "statefulset":
		ss, getErr := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
			return err
		}
		ss.Spec.Replicas = &r
		_, err = c.Client.AppsV1().StatefulSets(c.Namespace).Update(ctx, ss, metav1.UpdateOptions{})

	default:
		err = fmt.Errorf("Scale not supported for kind: %s", kind)
	}

	return err
}

// Restart triggers a rolling restart by updating the pod template annotation.
// Resource format: "kind/name" (e.g., "deployment/myapp")
func (c *Context) Restart(resource string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Restart",
		attribute.String("resource", resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid resource format %q, expected kind/name", resource)
		return err
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]
	ctx := context.Background()
	restartedAt := time.Now().Format(time.RFC3339)

	switch kind {
	case "deployment":
		deploy, getErr := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
			return err
		}
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = make(map[string]string)
		}
		deploy.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
		_, err = c.Client.AppsV1().Deployments(c.Namespace).Update(ctx, deploy, metav1.UpdateOptions{})

	case "statefulset":
		ss, getErr := c.Client.AppsV1().StatefulSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
			return err
		}
		if ss.Spec.Template.Annotations == nil {
			ss.Spec.Template.Annotations = make(map[string]string)
		}
		ss.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
		_, err = c.Client.AppsV1().StatefulSets(c.Namespace).Update(ctx, ss, metav1.UpdateOptions{})

	case "daemonset":
		ds, getErr := c.Client.AppsV1().DaemonSets(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
			return err
		}
		if ds.Spec.Template.Annotations == nil {
			ds.Spec.Template.Annotations = make(map[string]string)
		}
		ds.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
		_, err = c.Client.AppsV1().DaemonSets(c.Namespace).Update(ctx, ds, metav1.UpdateOptions{})

	default:
		err = fmt.Errorf("Restart not supported for kind: %s", kind)
	}

	return err
}

// Rollback rolls back a deployment to the previous revision.
// Resource format: "deployment/name"
func (c *Context) Rollback(resource string) (err error) {
	_, span := c.startSpan(context.Background(), "ilmari.Rollback",
		attribute.String("resource", resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	parts := strings.SplitN(resource, "/", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid resource format %q, expected kind/name", resource)
		return err
	}

	kind := strings.ToLower(parts[0])
	name := parts[1]

	if kind != "deployment" {
		err = fmt.Errorf("Rollback only supported for deployments, got: %s", kind)
		return err
	}

	ctx := context.Background()

	// Get deployment
	deploy, err := c.Client.AppsV1().Deployments(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Get ReplicaSets for this deployment
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to parse selector: %w", err)
	}

	rsList, err := c.Client.AppsV1().ReplicaSets(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return fmt.Errorf("failed to list replicasets: %w", err)
	}

	// Sort by revision - only include ReplicaSets with valid revision annotations
	type rsWithRevision struct {
		rs       appsv1.ReplicaSet
		revision int64
	}
	var revisions []rsWithRevision
	for _, rs := range rsList.Items {
		revStr := rs.Annotations["deployment.kubernetes.io/revision"]
		rev, err := strconv.ParseInt(revStr, 10, 64)
		if err != nil {
			msg := fmt.Sprintf("Warning: ReplicaSet %q in namespace %q has invalid or missing revision annotation: %q (error: %v); skipping", rs.Name, rs.Namespace, revStr, err)
			if c.t != nil {
				c.t.Log(msg)
			} else {
				fmt.Println(msg)
			}
			continue
		}
		revisions = append(revisions, rsWithRevision{rs: rs, revision: rev})
	}
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].revision > revisions[j].revision
	})

	// Ensure there are at least two revisions after sorting
	if len(revisions) < 2 {
		return fmt.Errorf("no previous revision to rollback to (found %d valid revisions)", len(revisions))
	}

	// Get the previous revision (index 1)
	previousRS := revisions[1].rs

	// Copy the pod template from previous revision
	deploy.Spec.Template = previousRS.Spec.Template

	// Update deployment
	_, err = c.Client.AppsV1().Deployments(c.Namespace).Update(ctx, deploy, metav1.UpdateOptions{})
	return err
}

// CanI checks if the current user has permission to perform an action.
// verb: "get", "list", "create", "update", "delete", "watch", etc.
// resource: "pods", "deployments", "services", etc.
func (c *Context) CanI(verb, resource string) (allowed bool, err error) {
	_, span := c.startSpan(context.Background(), "ilmari.CanI",
		attribute.String("verb", verb),
		attribute.String("resource", resource))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: c.Namespace,
				Verb:      verb,
				Resource:  resource,
			},
		},
	}

	result, err := c.Client.AuthorizationV1().SelfSubjectAccessReviews().Create(
		context.Background(), review, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}

	return result.Status.Allowed, nil
}
