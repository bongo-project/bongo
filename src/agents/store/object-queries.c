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

/**
 * Find the related conversation GUID for a given document.
 * Only really makes sense if the GUID passed is for an email document.
 * 
 * \param	client 	Store client we're operating for
 * \param	guid	GUID of the object we want to find the conversation for
 * \return	0 on failure, the GUID otherwise.
 */
uint64_t
SOQuery_ConversationGUIDForEmail(StoreClient *client, uint64_t guid)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	uint64_t conversation_guid = 0;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	query = "SELECT so.guid FROM links l LEFT JOIN storeobject so ON l.doc_guid = so.guid WHERE l.related_guid = ?1 AND so.collection_guid = ?2 AND so.type = 5;";
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto end;
	
	MsgSQLBindInt64(&stmt, 1, guid);
	MsgSQLBindInt64(&stmt, 2, STORE_CONVERSATIONS_GUID);
	
	// this assumes only one convo per mail. Should be ok..
	status = MsgSQLResults(client->storedb, &stmt);
	if (status > 0) {
		conversation_guid = MsgSQLResultInt64(&stmt, 0);
	}
end:
	MsgSQLFinalize(&stmt);
	
	return conversation_guid;
}

/**
 * Unlink store objects. Supply the document and the related document, and the
 * link between the two is removed. If either the document or related 
 * document is 0, it means "any document".
 * 
 * \param	client	Store client we're operating for
 * \param	document	GUID of document we want to relate to
 * \param	related	GUID of relative we want to unlink from
 * \param	any		If true, we remove anything linked to the document or related GUID
 * 				(only has an effect when document and related are supplied)
 * \return	0 if successful, -1 otherwise
 */
int
SOQuery_Unlink(StoreClient *client, uint64_t document, uint64_t related, BOOL any)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	
	if ((document == 0) || (related == 0)) return -1;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	if (document && related) {
		if (any == TRUE) {
			query = "DELETE FROM links WHERE doc_guid=?1 OR related_guid=?2;";
		} else {
			query = "DELETE FROM links WHERE doc_guid=?1 AND related_guid=?2;";
		}
	} else if (document) {
		query = "DELETE FROM links WHERE doc_guid=?1";
	} else {
		query = "DELETE FROM links WHERE related_guid=?1";
	}
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	if (document != 0) {
		MsgSQLBindInt64(&stmt, 1, document);
	} else {
		MsgSQLBindInt64(&stmt, 1, related);
	}
	if (document && related) MsgSQLBindInt64(&stmt, 2, related);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) goto abort;
	
	MsgSQLFinalize(&stmt);
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	return -1;
}
