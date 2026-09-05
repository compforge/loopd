const subscriptionKey = "loopd.subscriptions";

export interface StoredSubscription {
  taskID: string;
  lastEventID: string;
}

// One page subscription observes the conversation, including later actor output.
// taskID is a reconnect identity, not a business execution lifetime.
export function readSubscriptions(): Record<string, StoredSubscription> {
  try { return JSON.parse(localStorage.getItem(subscriptionKey) ?? "{}"); }
  catch { return {}; }
}

export function writeSubscription(conversationID: string, value: StoredSubscription) {
  localStorage.setItem(subscriptionKey, JSON.stringify({ ...readSubscriptions(), [conversationID]: value }));
}
