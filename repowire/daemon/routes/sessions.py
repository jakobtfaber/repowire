"""Session-targeted control endpoints."""

from __future__ import annotations

from typing import Literal

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from repowire.daemon.auth import require_auth
from repowire.daemon.deps import get_app_state
from repowire.daemon.peer_delivery import peer_delivery_from_state
from repowire.daemon.session_controls import (
    SessionBindingStoreUnavailableError,
    resolve_session_binding,
    resume_capability_for,
)
from repowire.daemon.session_resume import (
    ResumeUnavailableError,
    resume_target,
    target_from_resolution,
)
from repowire.daemon.websocket_transport import TransportError
from repowire.protocol.messages import AttachmentRef

router = APIRouter(tags=["sessions"])


class SessionNotifyRequest(BaseModel):
    """Fire-and-forget control message targeted by Repowire session ID."""

    from_peer: str = Field(default="dashboard", description="Sending peer or surface")
    text: str = Field(..., description="Notification/control text")
    attachments: list[AttachmentRef] = Field(default_factory=list)
    bypass_circle: bool = Field(
        default=True,
        description="Dashboard/API callers default to bypassing circle checks",
    )


class SessionNotifyResponse(BaseModel):
    """Response for a session-targeted notify control."""

    ok: bool = True
    repowire_session_id: str
    session_status: str
    capability: Literal["active_executor"]
    executor_peer_id: str
    executor_peer_name: str
    delivery_state: Literal["delivered", "queued"]
    delivered: bool
    queued: bool
    reason: str
    hook_delivery: dict | None = None


class SessionResumeRequest(BaseModel):
    """Inspect or request the compatible resume path for a Repowire session."""

    from_peer: str = Field(default="dashboard", description="Caller peer or surface")
    dry_run: bool = Field(
        default=True,
        description="Inspect capability when true; spawn backend resume when false",
    )
    profile: str | None = Field(default=None, description="Optional spawn profile")
    message: str | None = Field(default=None, description="Optional startup nudge")


class SessionResumeResponse(BaseModel):
    """Current resume capability for a session binding."""

    ok: bool = True
    repowire_session_id: str
    session_status: str
    status: Literal[
        "active_executor",
        "resume_available",
        "unsupported",
        "binding_unavailable",
    ]
    capability: Literal["active_executor", "supported", "unsupported", "unavailable"]
    message: str
    backend: str
    runtime_session_id: str | None = None
    runtime_source_uri: str | None = None
    executor_peer_id: str | None = None
    executor_peer_name: str | None = None
    resume_capability: dict = Field(default_factory=dict)
    action: Literal["inspect", "spawned"] = "inspect"
    spawned_display_name: str | None = None
    tmux_session: str | None = None
    pane_id: str | None = None


@router.post(
    "/sessions/{repowire_session_id}/controls/notify",
    response_model=SessionNotifyResponse,
)
async def notify_session(
    repowire_session_id: str,
    request: SessionNotifyRequest,
    _auth: str | None = Depends(require_auth),
) -> SessionNotifyResponse:
    """Send a notify/control message to the active executor for a session."""
    state = get_app_state()
    resolution = await _resolve_or_http(state, repowire_session_id)
    if not resolution.has_active_executor or resolution.executor is None:
        resume_status, capability, message = resume_capability_for(resolution)
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={
                "error": "session_executor_unavailable",
                "repowire_session_id": repowire_session_id,
                "session_status": resolution.binding.status,
                "status": resume_status,
                "executor_peer_id": resolution.executor_peer_id,
                "capability": capability,
                "message": message,
            },
        )

    try:
        delivery = await peer_delivery_from_state(
            config=state.config,
            registry=state.peer_registry,
            state=state,
        ).notify_result(
            from_peer=request.from_peer,
            to_peer=resolution.executor.peer_id,
            text=request.text,
            bypass_circle=request.bypass_circle,
            attachments=request.attachments,
        )
    except TransportError as exc:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail={
                "error": "transport_unavailable",
                "repowire_session_id": repowire_session_id,
                "executor_peer_id": resolution.executor.peer_id,
                "message": str(exc),
            },
        ) from exc
    except ValueError as exc:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={
                "error": "session_delivery_rejected",
                "repowire_session_id": repowire_session_id,
                "message": str(exc),
            },
        ) from exc

    return SessionNotifyResponse(
        repowire_session_id=repowire_session_id,
        session_status=resolution.binding.status,
        capability="active_executor",
        executor_peer_id=resolution.executor.peer_id,
        executor_peer_name=resolution.executor.display_name,
        delivery_state=delivery.delivery_state,
        delivered=delivery.delivered,
        queued=delivery.queued,
        reason=delivery.reason,
        hook_delivery=delivery.hook_delivery,
    )


@router.post(
    "/sessions/{repowire_session_id}/controls/resume",
    response_model=SessionResumeResponse,
)
async def resume_session(
    repowire_session_id: str,
    request: SessionResumeRequest,
    _auth: str | None = Depends(require_auth),
) -> SessionResumeResponse:
    """Inspect or execute backend-native resume for a session binding."""
    state = get_app_state()
    resolution = await _resolve_or_http(state, repowire_session_id)
    try:
        target = target_from_resolution(resolution)
        result = resume_target(
            target=target,
            spawn_service=getattr(state, "spawn_service", None) if not request.dry_run else None,
            dry_run=request.dry_run,
            profile=request.profile,
            message=request.message,
            circle="default",
        )
    except ResumeUnavailableError as exc:
        binding = resolution.binding
        http_status = (
            status.HTTP_503_SERVICE_UNAVAILABLE
            if exc.error == "spawn_service_unavailable"
            else
            status.HTTP_409_CONFLICT
            if exc.error in {"active_executor", "resume_unavailable"}
            else status.HTTP_422_UNPROCESSABLE_CONTENT
        )
        raise HTTPException(
            status_code=http_status,
            detail={
                "error": exc.error,
                "repowire_session_id": repowire_session_id,
                "session_status": binding.status,
                "status": exc.status,
                "capability": exc.capability,
                "message": exc.message,
                "resume_warning": exc.warning,
            },
        ) from exc

    return SessionResumeResponse(
        repowire_session_id=repowire_session_id,
        session_status=result.target.status,
        status=result.status,
        capability=result.capability,
        message=result.message,
        backend=result.target.backend.value,
        runtime_session_id=result.target.runtime_session_id,
        runtime_source_uri=result.target.runtime_source_uri,
        executor_peer_id=result.target.executor_peer_id,
        executor_peer_name=result.target.executor_peer_name,
        resume_capability=result.resume_capability,
        action=result.action,
        spawned_display_name=result.spawned_display_name,
        tmux_session=result.tmux_session,
        pane_id=result.pane_id,
    )


async def _resolve_or_http(state: object, repowire_session_id: str):
    try:
        resolution = await resolve_session_binding(
            state=state,
            repowire_session_id=repowire_session_id,
        )
    except SessionBindingStoreUnavailableError as exc:
        raise HTTPException(
            status_code=422,
            detail={
                "error": "session_bindings_unavailable",
                "message": str(exc),
            },
        ) from exc
    if resolution is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail={
                "error": "session_not_found",
                "repowire_session_id": repowire_session_id,
            },
        )
    return resolution
