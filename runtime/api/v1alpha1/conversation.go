package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const ConversationKind = "Conversation"

// ConversationParticipant carries a recipient-specific wake signal. A signal
// for B must not overwrite the signal for A when Kubernetes coalesces updates.
type ConversationParticipant struct {
	// +kubebuilder:validation:Enum=operator;harness
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// LatestMessageID is the newest committed message addressed to this actor.
	LatestMessageID string `json:"latestMessageID,omitempty"`
}

type ConversationSpec struct {
	// +listType=map
	// +listMapKey=kind
	// +listMapKey=key
	Participants []ConversationParticipant `json:"participants,omitempty"`
}

type ConversationListener struct {
	// +kubebuilder:validation:Enum=operator;harness
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
	// LastMessageID acknowledges receipt, not successful business execution.
	LastMessageID string `json:"lastMessageID,omitempty"`
}

type ConversationStatus struct {
	// +listType=map
	// +listMapKey=kind
	// +listMapKey=key
	Listeners []ConversationListener `json:"listeners,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=conv

// Conversation is the durable collaboration boundary, not a business Task.
// Its name equals the server conversation ID. Message bodies remain in SQL;
// Operators own their business resources and choose when to call Listen.
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

func (conversation *Conversation) LatestMessageID(kind, key string) string {
	for _, participant := range conversation.Spec.Participants {
		if participant.Kind == kind && participant.Key == key {
			return participant.LatestMessageID
		}
	}
	return ""
}

func (conversation *Conversation) LastMessageID(kind, key string) string {
	for _, listener := range conversation.Status.Listeners {
		if listener.Kind == kind && listener.Key == key {
			return listener.LastMessageID
		}
	}
	return ""
}
