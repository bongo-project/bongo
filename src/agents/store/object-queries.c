/**
 * object-queries.c : common SQL queries used in the object model.
 * 
 * This is a set of utility SQL queries that is used to help make the
 * object-model.c file a bit more concise.
 * 
 * Functions in here do not setup transactions, so the caller can setup
 * a transaction and call many of the functions in a row. These functions
 * should not be used outside of object-model.c
 */

#include <xpl.h>
#include <memmgr.h>
#include <msgapi.h>
#include "object-model.h"
#include "object-queries.h"

/**
 * Remove an object from the store by its guid
 * 
 * \param	client 	Store client we're operating for
 * \param	guid	GUID of the object we want to remove
 * \return	0 on success, -1 is 'no such guid', -2 is other failure
 */
int
SOQuery_RemoveSOByGUID(StoreClient *client, uint64_t guid)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int retcode = -2;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	query = "DELETE FROM storeobject WHERE guid=?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto end;
	
	MsgSQLBindInt64(&stmt, 1, guid);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	retcode = (status == 0)? 0 : -1;
	
end:
	MsgSQLFinalize(&stmt);
	return retcode;
}
