package controllers_test

import (
	"bytes"
	"errors"
	"github.com/rabbitmq/messaging-topology-operator/controllers"
	"github.com/rabbitmq/messaging-topology-operator/internal"
	"github.com/rabbitmq/messaging-topology-operator/rabbitmqclient"
	"github.com/rabbitmq/messaging-topology-operator/rabbitmqclient/rabbitmqclientfakes"
	"io/ioutil"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	topology "github.com/rabbitmq/messaging-topology-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("schema-replication-controller", func() {
	var replication topology.SchemaReplication
	var replicationName string

	JustBeforeEach(func() {
		replication = topology.SchemaReplication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      replicationName,
				Namespace: "default",
			},
			Spec: topology.SchemaReplicationSpec{
				UpstreamSecret: &corev1.LocalObjectReference{
					Name: "endpoints-secret", // created in 'BeforeSuite'
				},
				RabbitmqClusterReference: topology.RabbitmqClusterReference{
					Name: "example-rabbit",
				},
			},
		}
	})

	When("creation", func() {
		When("the RabbitMQ Client returns a HTTP error response", func() {
			BeforeEach(func() {
				replicationName = "test-replication-http-error"
				fakeRabbitMQClient.PutGlobalParameterReturns(&http.Response{
					Status:     "418 I'm a teapot",
					StatusCode: 418,
				}, errors.New("some HTTP error"))
			})

			It("sets the status condition to indicate a failure to reconcile", func() {
				Expect(client.Create(ctx, &replication)).To(Succeed())
				EventuallyWithOffset(1, func() []topology.Condition {
					_ = client.Get(
						ctx,
						types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace},
						&replication,
					)

					return replication.Status.Conditions
				}, statusEventsUpdateTimeout, 1*time.Second).Should(ContainElement(MatchFields(IgnoreExtras, Fields{
					"Type":    Equal(topology.ConditionType("Ready")),
					"Reason":  Equal("FailedCreateOrUpdate"),
					"Status":  Equal(corev1.ConditionFalse),
					"Message": ContainSubstring("some HTTP error"),
				})))
			})
		})

		When("the RabbitMQ Client returns a Go error response", func() {
			BeforeEach(func() {
				replicationName = "test-replication-go-error"
				fakeRabbitMQClient.PutGlobalParameterReturns(nil, errors.New("some go failure here"))
			})

			It("sets the status condition to indicate a failure to reconcile", func() {
				Expect(client.Create(ctx, &replication)).To(Succeed())
				EventuallyWithOffset(1, func() []topology.Condition {
					_ = client.Get(
						ctx,
						types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace},
						&replication,
					)

					return replication.Status.Conditions
				}, statusEventsUpdateTimeout, 1*time.Second).Should(ContainElement(MatchFields(IgnoreExtras, Fields{
					"Type":    Equal(topology.ConditionType("Ready")),
					"Reason":  Equal("FailedCreateOrUpdate"),
					"Status":  Equal(corev1.ConditionFalse),
					"Message": ContainSubstring("some go failure here"),
				})))
			})
		})
	})

	When("deletion", func() {
		JustBeforeEach(func() {
			fakeRabbitMQClient.PutGlobalParameterReturns(&http.Response{
				Status:     "201 Created",
				StatusCode: http.StatusCreated,
			}, nil)
			Expect(client.Create(ctx, &replication)).To(Succeed())
			EventuallyWithOffset(1, func() []topology.Condition {
				_ = client.Get(
					ctx,
					types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace},
					&replication,
				)

				return replication.Status.Conditions
			}, statusEventsUpdateTimeout, 1*time.Second).Should(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Type":   Equal(topology.ConditionType("Ready")),
				"Reason": Equal("SuccessfulCreateOrUpdate"),
				"Status": Equal(corev1.ConditionTrue),
			})))
		})

		When("the RabbitMQ Client returns a HTTP error response", func() {
			BeforeEach(func() {
				replicationName = "delete-replication-http-error"
				fakeRabbitMQClient.DeleteGlobalParameterReturns(&http.Response{
					Status:     "502 Bad Gateway",
					StatusCode: http.StatusBadGateway,
					Body:       ioutil.NopCloser(bytes.NewBufferString("Hello World")),
				}, nil)
			})

			It("raises an event to indicate a failure to delete", func() {
				Expect(client.Delete(ctx, &replication)).To(Succeed())
				Consistently(func() bool {
					err := client.Get(ctx, types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace}, &topology.SchemaReplication{})
					return apierrors.IsNotFound(err)
				}, statusEventsUpdateTimeout).Should(BeFalse())
				Expect(observedEvents()).To(ContainElement("Warning FailedDelete failed to delete schemareplication"))
			})
		})

		When("the RabbitMQ Client returns a Go error response", func() {
			BeforeEach(func() {
				replicationName = "delete-replication-go-error"
				fakeRabbitMQClient.DeleteGlobalParameterReturns(nil, errors.New("some error"))
			})

			It("publishes a 'warning' event", func() {
				Expect(client.Delete(ctx, &replication)).To(Succeed())
				Consistently(func() bool {
					err := client.Get(ctx, types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace}, &topology.SchemaReplication{})
					return apierrors.IsNotFound(err)
				}, statusEventsUpdateTimeout).Should(BeFalse())
				Expect(observedEvents()).To(ContainElement("Warning FailedDelete failed to delete schemareplication"))
			})
		})
	})

	When("a schema replication uses vault as secretBackend", func() {
		JustBeforeEach(func() {
			replicationName = "vault"
			replication = topology.SchemaReplication{
				ObjectMeta: metav1.ObjectMeta{
					Name:      replicationName,
					Namespace: "default",
				},
				Spec: topology.SchemaReplicationSpec{
					SecretBackend: topology.SecretBackend{Vault: &topology.VaultSpec{SecretPath: "rabbitmq"}},
					Endpoints:     "test:12345",
					RabbitmqClusterReference: topology.RabbitmqClusterReference{
						Name:      "example-rabbit",
						Namespace: "default",
					},
				},
			}

			fakeRabbitMQClient.PutGlobalParameterReturns(&http.Response{
				Status:     "201 Created",
				StatusCode: http.StatusCreated,
			}, nil)
		})

		AfterEach(func() {
			rabbitmqclient.SecretStoreClientProvider = rabbitmqclient.GetSecretStoreClient
		})

		It("set schema sync parameters with generated correct endpoints", func() {
			fakeSecretStoreClient := &rabbitmqclientfakes.FakeSecretStoreClient{}
			fakeSecretStoreClient.ReadCredentialsReturns("a-user-in-vault", "test", nil)
			rabbitmqclient.SecretStoreClientProvider = func() (rabbitmqclient.SecretStoreClient, error) {
				return fakeSecretStoreClient, nil
			}

			Expect(client.Create(ctx, &replication)).To(Succeed())
			Eventually(func() []topology.Condition {
				_ = client.Get(
					ctx,
					types.NamespacedName{Name: replication.Name, Namespace: replication.Namespace},
					&replication,
				)
				return replication.Status.Conditions
			}, statusEventsUpdateTimeout, 1*time.Second).Should(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Type":   Equal(topology.ConditionType("Ready")),
				"Reason": Equal("SuccessfulCreateOrUpdate"),
				"Status": Equal(corev1.ConditionTrue),
			})))

			parameter, endpoints := fakeRabbitMQClient.PutGlobalParameterArgsForCall(1)
			Expect(parameter).To(Equal(controllers.SchemaReplicationParameterName))
			Expect(endpoints.(internal.UpstreamEndpoints).Username).To(Equal("a-user-in-vault"))
			Expect(endpoints.(internal.UpstreamEndpoints).Password).To(Equal("test"))
			Expect(endpoints.(internal.UpstreamEndpoints).Endpoints).To(ConsistOf("test:12345"))
		})
	})
})
