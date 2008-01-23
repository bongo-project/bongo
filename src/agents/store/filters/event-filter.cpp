#include <config.h>

#include "stored.h"
#include "filter.h"

static void
AddObject(StoreClient *client, 
          DStoreDocInfo *info, 
          BongoCalObject *cal,
          Document *doc)
{
    FilterAddText(doc, _T("summary"), BongoCalObjectGetSummary(cal));
    FilterAddText(doc, _T("location"), BongoCalObjectGetLocation(cal));
    FilterAddText(doc, _T("description"), BongoCalObjectGetDescription(cal));
}

int 
FilterEvent(StoreClient *client, 
            DStoreDocInfo *info, 
            LuceneIndex *index,
            char *path)
{
    BongoJsonNode *node;
    BongoJsonObject *obj;
    BongoCalObject *cal;
    
    if (GetJson(client, info, &node, path) != BONGO_JSON_OK || node->type != BONGO_JSON_OBJECT) {
        return -1;
    }

    obj = BongoJsonNodeAsObject(node);

    cal = BongoCalObjectNew(obj);

    Document *doc = new Document();
    
    FilterAddSummaryInfo(doc, info);
    FilterAddKeyword(doc, _T("type"), "event", false);

    if (obj) {
        AddObject(client, info, cal, doc);
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
