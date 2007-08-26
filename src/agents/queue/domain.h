#ifndef DOMAIN_H
#define DOMAIN_H


typedef struct _QDBQueryResults {

} QDBQueryResults;


int QDBStartup(int minimum, int maximum);
void QDBShutdown(void);
void *QDBHandleAlloc(void);
void QDBHandleRelease(void *handle);
int QDBAdd(void *handle, unsigned char *domain, unsigned long queueID, int queue);
int QDBRemoveID(void *handle, unsigned long queueID);
int QDBRemoveDomain(void *handle, unsigned char *domain);
int QDBSearchID(void *handle, unsigned long queueID);
int QDBSearchDomain(void *handle, unsigned char *domain);
int QDBQuery(void *handle, unsigned char *query, QDBQueryResults *results);
int QDBDump(unsigned char *domain);
void QDBSummarizeQueue(void);

#endif
