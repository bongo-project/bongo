#ifndef OBJECT_QUERIES_H
#define OBJECT_QUERIES_H

int SOQuery_RemoveSOByGUID(StoreClient *client, uint64_t guid);

int SOQuery_Unlink(StoreClient *client, uint64_t document, uint64_t related, BOOL any);

uint64_t SOQuery_ConversationGUIDForEmail(StoreClient *client, uint64_t guid);

#endif // OBJECT_QUERIES_H
