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
