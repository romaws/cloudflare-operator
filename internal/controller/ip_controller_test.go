/*
Copyright 2025 containeroo

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

var (
	requestHeader     string
	requestAuthHeader string
)

func StartIPSource() {
	http.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.1.1.1"))
	})
	http.HandleFunc("/invalid", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("invalid"))
	})
	http.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ip":"1.1.1.1"}`))
	})
	http.HandleFunc("/header", func(w http.ResponseWriter, r *http.Request) {
		requestHeader = r.Header.Get("X-Test")
		requestAuthHeader = r.Header.Get("X-Auth-Test")
		_, _ = w.Write([]byte("1.1.1.1"))
	})

	_ = http.ListenAndServe(":8080", nil)
}

func TestIPReconciler_reconcileIP(t *testing.T) {
	g := NewWithT(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"X-Auth-Test": []byte("auth-test"),
		},
	}

	ip := &cloudflareoperatoriov1.IP{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ip",
		},
		Spec: cloudflareoperatoriov1.IPSpec{
			Type: "dynamic",
		},
	}

	r := &IPReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(secret).
			Build(),
	}

	go StartIPSource()

	t.Run("reconcile dynamic ip plain text", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "http://localhost:8080/plain",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile dynamic ip plain text error invalid ip", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "http://localhost:8080/invalid",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "ip from source http://localhost:8080/invalid is invalid: invalid"),
		}))
	})

	t.Run("reconcile dynamic ip jq filter", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:              "http://localhost:8080/json",
			ResponseJQFilter: ".ip",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile dynamic ip regex", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:                 "http://localhost:8080/json",
			PostProcessingRegex: "([0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+)",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile dynamic ip with header", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "http://localhost:8080/header",
			RequestHeaders: &apiextensionsv1.JSON{
				Raw: []byte(`{"X-Test":"test"}`),
			},
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
		g.Expect(requestHeader).To(Equal("test"))
	})

	t.Run("reconcile dynamic ip with header from secret", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "http://localhost:8080/header",
			RequestHeadersSecretRef: corev1.SecretReference{
				Name:      "secret",
				Namespace: "default",
			},
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
		g.Expect(requestAuthHeader).To(Equal("auth-test"))
	})

	t.Run("reconcile static ip", func(t *testing.T) {
		ip.Spec.Type = "static"
		ip.Spec.Address = "1.1.1.1"

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionTypeReady, "IP is ready"),
		}))
		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile static ip error no address", func(t *testing.T) {
		ip.Spec.Address = ""

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "address is required for static IPs"),
		}))
	})

	t.Run("reconcile static ip error invalid address", func(t *testing.T) {
		ip.Spec.Address = "invalid"

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "IP address \"invalid\" is not valid"),
		}))
	})
}

func TestIPReconciler_getIPSourceClosesConnections(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	open := map[net.Conn]struct{}{}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.1.1.1"))
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case http.StateNew:
			open[conn] = struct{}{}
		case http.StateClosed, http.StateHijacked:
			delete(open, conn)
		}
	}
	server.Start()
	defer server.Close()

	r := &IPReconciler{HTTPClientTimeout: 5 * time.Second}
	source := cloudflareoperatoriov1.IPSpecIPSources{URL: server.URL}

	for range 20 {
		address, err := r.getIPSource(context.TODO(), source)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(address).To(Equal("1.1.1.1"))
	}

	g.Eventually(func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(open)
	}, 2*time.Second, 50*time.Millisecond).Should(BeZero())
}
