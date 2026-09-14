export interface Project {
  id: string
  name: string
  profile_id: string
  profile_version: string
  accepted_input_media_types: string[]
  created_at: string
  updated_at: string
}

export interface InputFile {
  id: string
  project_id: string
  display_name: string
  media_type: string
  size_bytes: number
  sha256_digest: string
  created_at: string
}

export interface Conversation {
  id: string
  project_id: string
  title: string
  active_sdk_session_id: string | null
  created_at: string
  updated_at: string
}

export interface ErrorEnvelope {
  code: string
  message: string
  details: Record<string, unknown> | null
  request_id: string
}

export interface Message {
  id: string
  conversation_id: string
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

export interface Run {
  id: string
  conversation_id: string
  trigger_message_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'
  phase: 'preparing' | 'agent' | 'publishing' | null
  // Runtime failures carry code/message/retryable without HTTP request metadata.
  error: { code: string; message: string; request_id?: string; details?: Record<string, unknown> | null; retryable?: boolean } | null
  source_sdk_session_id: string | null
  candidate_sdk_session_id: string | null
  finalized_at: string | null
  created_at: string
  updated_at: string
}

export interface RunEvent {
  run_id: string
  sequence: number
  type: string
  payload: Record<string, unknown>
  occurred_at: string
}

export interface SubmitMessageResult { message: Message; run: Run }

export interface Artifact {
  id: string
  run_id: string
  title: string
  type: 'html' | 'markdown' | 'image' | 'data'
  entry_path: string
  is_primary: boolean
  gateway_url: string
  created_at: string
}
