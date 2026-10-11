package v4

// RFC 8474 OBJECTID schema additions (v4).

const MailboxesFieldObjectID = "object_id"

const MessagesFieldObjectID = "object_id"
const MessagesFieldThreadID = "thread_id"

const MailboxObjectIDIndex = "mailboxes_object_id_key"
const MessageObjectIDIndex = "messages_object_id_key"
const MessageThreadIDIndex = "messages_thread_id_index"

const ThreadTokensTableName = "message_thread_tokens"
const ThreadTokensFieldMessageID = "message_id"
const ThreadTokensFieldToken = "token"
const ThreadTokensMessageIDIndex = "message_thread_tokens_message_id_index"
const ThreadTokensTokenIndex = "message_thread_tokens_token_index"
