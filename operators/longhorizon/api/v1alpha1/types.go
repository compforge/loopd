package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Reference struct {
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=128
	UID types.UID `json:"uid"`
}

// MessageReference retains visible context without copying transcripts into CRDs.
type MessageReference struct {
	// +kubebuilder:validation:MaxLength=128
	ConversationID string `json:"conversationID"`
	// +kubebuilder:validation:MaxLength=128
	MessageID string `json:"messageID"`
}

type RunSpec struct {
	// +kubebuilder:validation:MaxItems=20
	ContextMessages []MessageReference `json:"contextMessages,omitempty"`
	Conversation    Reference          `json:"conversation"`
	// +kubebuilder:validation:MaxLength=128
	WorkspaceID string `json:"workspaceID"`
	// +kubebuilder:validation:MaxLength=128
	UserKey    string      `json:"userKey"`
	DeadlineAt metav1.Time `json:"deadlineAt"`
	// +kubebuilder:validation:MaxLength=128
	InputMessageID string `json:"inputMessageID"`
	// +kubebuilder:validation:MaxLength=16000
	Goal string `json:"goal"`
	// +kubebuilder:default=25
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MaxRounds int32 `json:"maxRounds"`
}

type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Decision is Manager's bounded control output, not a Harness checkpoint.
type Decision struct {
	Next       string   `json:"next"`
	Summary    string   `json:"summary"`
	Plan       string   `json:"plan,omitempty"`
	Contract   string   `json:"contract,omitempty"`
	Question   string   `json:"question,omitempty"`
	Choices    []Choice `json:"choices,omitempty"`
	AllowOther bool     `json:"allow_other,omitempty"`
}

type RoundSummary struct {
	Index     int32      `json:"index"`
	Decision  string     `json:"decision"`
	Summary   string     `json:"summary"`
	Messages  []string   `json:"messages,omitempty"`
	Execution *Reference `json:"execution,omitempty"`
	Audit     *Reference `json:"audit,omitempty"`
}

type Verdict struct {
	Complete  bool   `json:"complete"`
	Integrity string `json:"integrity"`
	TaskState string `json:"task_state"`
	Evidence  string `json:"evidence"`
	Feedback  string `json:"feedback"`
}

type AuditEvidence struct {
	Execution       Reference `json:"execution"`
	ContractVersion int64     `json:"contractVersion"`
	MessageID       string    `json:"messageID"`
	Verdict         Verdict   `json:"verdict"`
}

type RunStatus struct {
	// InputThrough is the contiguous prefix checkpointed by this Run.
	InputThrough string `json:"inputThrough,omitempty"`
	// +kubebuilder:validation:MaxItems=100
	InputMessageIDs  []string       `json:"inputMessageIDs,omitempty"`
	FinishedAt       *metav1.Time   `json:"finishedAt,omitempty"`
	Phase            string         `json:"phase,omitempty"`
	Round            int32          `json:"round,omitempty"`
	Budget           int32          `json:"budget,omitempty"`
	Failures         int32          `json:"failures,omitempty"`
	Contract         string         `json:"contract,omitempty"`
	ContractVersion  int64          `json:"contractVersion,omitempty"`
	TaskState        string         `json:"taskState,omitempty"`
	Guidance         string         `json:"guidance,omitempty"`
	Decision         *Decision      `json:"decision,omitempty"`
	Execution        *Reference     `json:"execution,omitempty"`
	Audit            *Reference     `json:"audit,omitempty"`
	LastAudit        *AuditEvidence `json:"lastAudit,omitempty"`
	HumanMessageID   string         `json:"humanMessageID,omitempty"`
	HumanReason      string         `json:"humanReason,omitempty"`
	ManagerMessageID string         `json:"managerMessageID,omitempty"`
	FinalMessageID   string         `json:"finalMessageID,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	ConsumedThrough  int32          `json:"consumedThrough,omitempty"`
	// +kubebuilder:validation:MaxItems=50
	Rounds []RoundSummary `json:"rounds,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
type Run struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="run inputs are immutable"
	Spec   RunSpec   `json:"spec"`
	Status RunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Run `json:"items"`
}

type ExecutionSpec struct {
	Run             Reference `json:"run"`
	Round           int32     `json:"round"`
	ContractVersion int64     `json:"contractVersion"`
	// +kubebuilder:validation:MaxLength=16000
	Contract string `json:"contract"`
	// +kubebuilder:validation:MaxLength=16000
	Plan string `json:"plan"`
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=128
	ReportMessageIDs []string `json:"reportMessageIDs,omitempty"`
}
type WorkStatus struct {
	Phase     string `json:"phase,omitempty"`
	CallID    string `json:"callID,omitempty"`
	MessageID string `json:"messageID,omitempty"`
	Error     string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
type Execution struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="execution inputs are immutable"
	Spec   ExecutionSpec `json:"spec"`
	Status WorkStatus    `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ExecutionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Execution `json:"items"`
}

type AuditSpec struct {
	Run             Reference `json:"run"`
	Round           int32     `json:"round"`
	ContractVersion int64     `json:"contractVersion"`
	// +kubebuilder:validation:MaxLength=16000
	Contract  string    `json:"contract"`
	Execution Reference `json:"execution"`
	// +kubebuilder:validation:MaxLength=128
	ExecutionMessageID string `json:"executionMessageID"`
}
type AuditStatus struct {
	WorkStatus `json:",inline"`
	Verdict    *Verdict `json:"verdict,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
type Audit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="audit inputs are immutable"
	Spec   AuditSpec   `json:"spec"`
	Status AuditStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AuditList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Audit `json:"items"`
}
