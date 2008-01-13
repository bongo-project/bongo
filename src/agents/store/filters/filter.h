#ifndef FILTER_H
#define FILTER_H

#include "stored.h"
#include "lucene-index.h"

int FilterDocument(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index, char *path);

int FilterAddSummaryInfo(Document *doc, DStoreDocInfo *info);
int FilterAddText(Document *doc, 
                  const TCHAR *fieldName, 
                  const char *value, 
                  bool includeInEverything = true);
int FilterAddTextWithBoost(Document *doc, 
                           const TCHAR *fieldName, 
                           const char *value, 
                           float boost, 
                           bool includeInEverything = true);
int FilterAddKeyword(Document *doc, 
                     const TCHAR *fieldName, 
                     const char *value, 
                     bool includeInEverything = true);

int FilterAddDate(Document *doc,
                  const TCHAR *fieldName,
                  uint64_t date);

int FilterAddJsonString(Document *doc, 
                        const TCHAR *fieldName, 
                        BongoJsonObject *obj,
                        const char *childName, 
                        bool includeInEverything = true);

int FilterMail(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index, char *path);
int FilterContact(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index, char *path);
int FilterEvent(StoreClient *client, DStoreDocInfo *info, LuceneIndex *index, char *path);

#endif

