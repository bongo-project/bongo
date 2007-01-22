#include <config.h>

#include "stored.h"
#include "filter.h"

#include <bongojson.h>

static void
AddObject(StoreClient *client, 
          DStoreDocInfo *info, 
          BongoJsonObject *obj, 
          Document *doc)
{
    BongoArray *array;
    struct json_object *child;
    const char *val;

    FilterAddJsonString(doc, _T("name"), obj, "fn");
    FilterAddJsonString(doc, _T("notes"), obj, "notes");
    
    if ((BongoJsonObjectGetArray(obj, "email", &array)) == BONGO_JSON_OK) {
        unsigned int i;
        for (i = 0; i < array->len; i++) {
            BongoJsonObject *address;
            if (BongoJsonArrayGetObject(array, i, &address) == BONGO_JSON_OK) {
                FilterAddJsonString(doc, _T("email"), address, "value");
            }
        }
    }
    
    if ((BongoJsonObjectGetArray(obj, "tel", &array)) == BONGO_JSON_OK) {
        unsigned int i;
        for (i = 0; i < array->len; i++) {
            BongoJsonObject *address;
            if (BongoJsonArrayGetObject(array, i, &address) == BONGO_JSON_OK) {
                FilterAddJsonString(doc, _T("tel"), address, "value");
            }
        }
    }       
}

int 
FilterContact(StoreClient *client, 
              DStoreDocInfo *info, 
              LuceneIndex *index)
{
    BongoJsonNode *node;
    BongoJsonObject *obj;
    
    if (GetJson(client, info, &node) != BONGO_JSON_OK || node->type != BONGO_JSON_OBJECT) {
        return -1;
    }
    
    obj = BongoJsonNodeAsObject(node);

    Document *doc = new Document();
    
    FilterAddSummaryInfo(doc, info);
    FilterAddKeyword(doc, _T("type"), "addressbook", false);

    if (obj) {
        AddObject(client, info, obj, doc);
    }

    BongoJsonNodeFree(node);

    int ret;
    try {
        index->GetIndexWriter()->addDocument(doc);
        ret = 0;
    } catch (...) {
        ret = 1;
    }

    delete doc;
    
    return ret;
}
