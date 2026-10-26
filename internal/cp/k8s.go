package cp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/app-plat/app-plat/internal/store"
	"github.com/app-plat/app-plat/pkg/appyaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

func (s *Server) kube() (dynamic.Interface, error) {
	if s.dyn != nil {
		return s.dyn, nil
	}
	var cfg *rest.Config
	var err error
	if s.cfg.InCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		).ClientConfig()
	}
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	s.dyn = dyn
	s.mapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	return dyn, nil
}

func (s *Server) applyObj(ctx context.Context, obj *unstructured.Unstructured) error {
	dyn, err := s.kube()
	if err != nil {
		return err
	}
	gvk := obj.GroupVersionKind()
	mapping, err := s.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		s.mapper.Reset()
		mapping, err = s.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("map %s: %w", gvk.String(), err)
		}
	}
	ns := obj.GetNamespace()
	ri := dyn.Resource(mapping.Resource)
	var dr dynamic.ResourceInterface = ri
	if mapping.Scope.Name() == "namespace" {
		if ns == "" {
			ns = s.cfg.Namespace
			obj.SetNamespace(ns)
		}
		dr = ri.Namespace(ns)
	}
	_, err = dr.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{FieldManager: "app-plat", Force: true})
	return err
}

func (s *Server) deleteObj(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) {
	dyn, err := s.kube()
	if err != nil {
		return
	}
	_ = dyn.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func (s *Server) applyApp(ctx context.Context, app store.App) error {
	res, err := s.store.GetResources(ctx, app.ID)
	if err != nil {
		return err
	}
	userEnv, err := s.store.GetEnv(ctx, app.ID)
	if err != nil {
		return err
	}
	env := s.platformEnv(app, res)
	for _, v := range userEnv {
		if _, reserved := env[v.Key]; reserved {
			continue
		}
		env[v.Key] = v.Value
	}
	name := "app-" + app.Slug
	labels := map[string]any{
		"app-plat.io/role": "user-app",
		"app-plat.io/slug": app.Slug,
		"app":              name,
	}
	ns := s.cfg.Namespace
	if err := s.applyObj(ctx, obj("v1", "ServiceAccount", name, ns, nil, nil)); err != nil {
		return err
	}
	if err := s.applyObj(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   objectMeta(name+"-env", ns, labels),
		"stringData": env,
	}}); err != nil {
		return err
	}
	if err := s.applyObj(ctx, deployment(name, ns, app, labels, s.cfg.ImagePullPolicy, env)); err != nil {
		return err
	}
	if err := s.applyObj(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   objectMeta(name, ns, labels),
		"spec": map[string]any{
			"selector": map[string]any{"app-plat.io/slug": app.Slug},
			"ports": []any{map[string]any{
				"name": "http", "port": int64(app.Port), "targetPort": int64(app.Port),
			}},
		},
	}}); err != nil {
		return err
	}
	host := app.Slug + "." + s.cfg.AppsDomain
	if err := s.applyObj(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "VirtualService",
		"metadata":   objectMeta(name, ns, labels),
		"spec": map[string]any{
			"hosts":    []any{host},
			"gateways": []any{"app-plat-gateway"},
			"http": []any{map[string]any{
				"route": []any{map[string]any{
					"destination": map[string]any{
						"host": name + "." + ns + ".svc.cluster.local",
						"port": map[string]any{"number": int64(app.Port)},
					},
				}},
			}},
		},
	}}); err != nil {
		return err
	}
	if err := s.applyObj(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1",
		"kind":       "AuthorizationPolicy",
		"metadata":   objectMeta(name, ns, labels),
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app-plat.io/slug": app.Slug}},
			"action":   "ALLOW",
			"rules": []any{map[string]any{
				"from": []any{map[string]any{"source": map[string]any{"namespaces": []any{"istio-system"}}}},
			}},
		},
	}}); err != nil {
		return err
	}
	return s.applyObj(ctx, networkPolicy(name, ns, app))
}

func envHash(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(env[k]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func deployment(name, ns string, app store.App, labels map[string]any, pullPolicy string, env map[string]string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   objectMeta(name, ns, labels),
		"spec": map[string]any{
			"replicas": int64(1),
			"selector": map[string]any{"matchLabels": map[string]any{"app-plat.io/slug": app.Slug}},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": labels,
					"annotations": map[string]any{
						"app-plat.io/image":  app.Image,
						"app-plat.io/config": envHash(env),
					},
				},
				"spec": map[string]any{
					"serviceAccountName": name,
					"containers": []any{map[string]any{
						"name":            "app",
						"image":           app.Image,
						"imagePullPolicy": pullPolicy,
						"ports":           []any{map[string]any{"containerPort": int64(app.Port), "name": "http"}},
						"envFrom":         []any{map[string]any{"secretRef": map[string]any{"name": name + "-env"}}},
						"readinessProbe": map[string]any{
							"httpGet":             map[string]any{"path": "/healthz", "port": int64(app.Port)},
							"initialDelaySeconds": int64(2),
							"periodSeconds":       int64(5),
						},
					}},
				},
			},
		},
	}}
}

func networkPolicy(name, ns string, app store.App) *unstructured.Unstructured {
	egress := []any{
		map[string]any{"ports": []any{
			map[string]any{"port": int64(53), "protocol": "UDP"},
			map[string]any{"port": int64(53), "protocol": "TCP"},
		}},
		map[string]any{"to": []any{map[string]any{"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": "istio-system"},
		}}}},
	}
	if appyaml.Has(app.Persistence, "postgresql") {
		egress = append(egress, podEgress("pgbouncer", 5432))
	}
	if appyaml.Has(app.Persistence, "keyvalue") {
		egress = append(egress, podEgress("valkey", 6379))
	}
	if appyaml.Has(app.Persistence, "blob") {
		egress = append(egress, podEgress("blob-gateway", 9000))
	}
	egress = append(egress, podEgress("llm-gateway", 8083))
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   objectMeta(name, ns, nil),
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app-plat.io/slug": app.Slug}},
			"policyTypes": []any{"Ingress", "Egress"},
			"ingress": []any{
				map[string]any{
					"from": []any{map[string]any{"namespaceSelector": map[string]any{
						"matchLabels": map[string]any{"kubernetes.io/metadata.name": "istio-system"},
					}}},
					"ports": []any{map[string]any{"port": int64(app.Port), "protocol": "TCP"}},
				},
				map[string]any{"ports": []any{
					map[string]any{"port": int64(15008), "protocol": "TCP"},
					map[string]any{"port": int64(15090), "protocol": "TCP"},
				}},
			},
			"egress": egress,
		},
	}}
}

func podEgress(app string, port int) map[string]any {
	return map[string]any{
		"to": []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": app}}}},
		"ports": []any{map[string]any{"port": int64(port), "protocol": "TCP"}},
	}
}

func (s *Server) removeApp(ctx context.Context, app store.App) error {
	name := "app-" + app.Slug
	ns := s.cfg.Namespace
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "virtualservices"}, ns, name)
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "authorizationpolicies"}, ns, name)
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, ns, name)
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, ns, name)
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, ns, name)
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, ns, name+"-env")
	s.deleteObj(ctx, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, ns, name)
	s.dropPersistence(ctx, app)
	return nil
}

func obj(apiVersion, kind, name, ns string, labels map[string]any, spec map[string]any) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   objectMeta(name, ns, labels),
	}
	if spec != nil {
		o["spec"] = spec
	}
	return &unstructured.Unstructured{Object: o}
}

func objectMeta(name, ns string, labels map[string]any) map[string]any {
	m := map[string]any{"name": name, "namespace": ns}
	if labels != nil {
		m["labels"] = labels
	}
	return m
}
