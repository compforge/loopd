package v1alpha1

import (
	"crypto/sha256"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ConversationKind = "Conversation"

// ConversationParticipant carries a recipient-specific wake signal. A signal
// for B must not overwrite the signal for A when Kubernetes coalesces updates.
type ConversationParticipant struct {
	// +kubebuilder:validation:Enum=operator;harness
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// EndOffset is the newest database message notified to this actor.
	EndOffset string `json:"endOffset,omitempty"`
}

type ConversationSpec struct {
	// +listType=map
	// +listMapKey=kind
	// +listMapKey=key
	Participants []ConversationParticipant `json:"participants,omitempty"`
}

type ConversationConsumer struct {
	// +kubebuilder:validation:Enum=operator;harness
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// Committed is the last committed message, exclusive on the next read.
	Committed string `json:"committed,omitempty"`
	// Position is diagnostic and bounds commits; recovery never skips
	// messages based on this field because a Poll response may have been lost.
	Position string `json:"position,omitempty"`
}

type ConversationStatus struct {
	// +listType=map
	// +listMapKey=kind
	// +listMapKey=key
	Consumers []ConversationConsumer `json:"consumers,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=conv

// Conversation is the durable collaboration boundary, not a business Task.
// Its name equals the server conversation ID. Message bodies remain in SQL;
// Operators own their business resources and choose when to call Poll.
type Conversation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ConversationSpec   `json:"spec"`
	Status            ConversationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ConversationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Conversation `json:"items"`
}

func (conversation *Conversation) EndOffset(kind, key string) string {
	for _, participant := range conversation.Spec.Participants {
		if participant.Kind == kind && participant.Key == key {
			return participant.EndOffset
		}
	}
	return ""
}

func (conversation *Conversation) Committed(kind, key string) string {
	for _, consumer := range conversation.Status.Consumers {
		if consumer.Kind == kind && consumer.Key == key {
			return consumer.Committed
		}
	}
	return ""
}

// WakeAnnotation identifies an actor-specific message revision notification.
// EndOffset alone cannot wake a reader when an earlier streamed message finishes.
func WakeAnnotation(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + key))
	return fmt.Sprintf("loopd.compforge.io/wake-%x", sum[:16])
}

func (conversation *Conversation) Wake(kind, key string) string {
	return conversation.Annotations[WakeAnnotation(kind, key)]
}
