#include "stored.h"
#include "messages.h"

CCode
AccountCreate(StoreClient *client, char *user, char *password)
{
	if (0 != MsgAuthFindUser(user))
		// account already exists
		return ConnWriteStr(client->conn, MSG4224NOUSER);
	
	MsgAuthAddUser(user);
	if (password != NULL)
		MsgAuthSetPassword(user, password);
	
	return ConnWriteStr(client->conn, MSG1000OK);
}

CCode
AccountDelete(StoreClient *client, char *user)
{
	if (0 == MsgAuthFindUser(user))
		return ConnWriteStr(client->conn, MSG4224NOUSER);
	
	// FIXME: MsgAuth doesn't support this (yet?)
	return ConnWriteStr(client->conn, MSG4224NOUSER);
}

CCode
AccountList(StoreClient *client)
{
	char **list, **ptr;
	
	if (0 == MsgAuthUserList(&list))
		// no users on system.
		return ConnWriteStr(client->conn, MSG1000OK);
	
	ptr = list;
	while (*ptr != 0) {
		ConnWriteF(client->conn, "2001 %s\r\n", *ptr);
		ptr++;
	}
	
	MsgAuthUserListFree(&list);
	
	return ConnWriteStr(client->conn, MSG1000OK);
}
