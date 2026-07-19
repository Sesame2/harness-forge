# Harness Forge

Harness Forge turns conversations and project inputs into immutable, viewable artifacts through a managed Agent runtime. This glossary defines the shared product language used across the control plane, Runtime, Web interface, and contracts.

## Language

**Profile**:
A versioned definition of one Harness application behavior, including its prompt, tool policy, and Artifact rules.
_Avoid_: Plugin, App

**Project**:
A durable container for one Profile, its Input Files, and its Conversations.
_Avoid_: Workspace

**Input File**:
An immutable user-uploaded object owned by a Project.
_Avoid_: Upload, dataset

**Conversation**:
A product-level chat inside one Project, with ordered Messages and one active SDK Session pointer.
_Avoid_: Session, thread

**Message**:
A user or Agent message visible inside a Conversation.
_Avoid_: Prompt, event

**Run**:
One execution attempt triggered by a user Message.
_Avoid_: Task, job

**SDK Session**:
Claude Agent SDK’s internal conversation context used to resume Agent reasoning.
_Avoid_: Session

**Run Event**:
An append-only progress, text, tool, phase, or diagnostic observation emitted during a Run.
_Avoid_: Message, log

**Artifact**:
An immutable, independently viewable output published by a successful Run.
_Avoid_: Output, result, file

**Workspace**:
An ephemeral filesystem directory materialized for one Run.
_Avoid_: Project Workspace
