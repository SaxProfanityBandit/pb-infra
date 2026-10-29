from __future__ import annotations

from typing import Any, Optional

from synapse.module_api import ModuleApi
from synapse.types import RoomAlias, UserID


class CovenRoomCreatePolicy:
    def __init__(self, config: dict[str, Any], api: ModuleApi) -> None:
        self._api = api
        self._allowed = set(config.get("allowed_localparts") or ["sax"])
        api.register_spam_checker_callbacks(
            user_may_create_room=self.user_may_create_room,
            user_may_create_room_alias=self.user_may_create_room_alias,
            user_may_publish_room=self.user_may_publish_room,
        )

    @staticmethod
    def parse_config(config: dict[str, Any]) -> dict[str, Any]:
        return config

    async def _may_manage_rooms(self, user_id: str) -> bool:
        if await self._api.is_server_admin(user_id):
            return True
        try:
            localpart = UserID.from_string(user_id).localpart
        except Exception:
            return False
        return localpart in self._allowed

    async def user_may_create_room(
        self, user_id: str, room_config: Optional[dict[str, Any]] = None
    ) -> bool:
        return await self._may_manage_rooms(user_id)

    async def user_may_create_room_alias(
        self, user_id: str, room_alias: RoomAlias
    ) -> bool:
        return await self._may_manage_rooms(user_id)

    async def user_may_publish_room(self, user_id: str, room_id: str) -> bool:
        return await self._may_manage_rooms(user_id)
