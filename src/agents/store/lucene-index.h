#ifndef LUCENE_INDEX_H
#define LUCENE_INDEX_H

#include <xpl.h>
#include <bongoutil.h>
#include <CLucene.h>

using namespace lucene::index;
using namespace lucene::util;
using namespace lucene::store;
using namespace lucene::search;
using namespace lucene::document;
using namespace lucene::queryParser;
using namespace lucene::analysis;
using namespace lucene::analysis::standard;

class LuceneIndex {
public :
    LuceneIndex();
    ~LuceneIndex();

    int Init(const char *basePath);

    IndexWriter *GetIndexWriter();
    IndexReader *GetIndexReader();
    IndexSearcher *GetIndexSearcher();
    QueryParser *GetQueryParser();

private :
    char *basePath;
    Analyzer *analyzer;
    IndexWriter *writer;
    IndexReader *reader;
    IndexSearcher *searcher;
    QueryParser *queryParser;
    Directory *directory;

    Directory *GetDirectory();
    Analyzer *GetAnalyzer();

    static BongoHashtable *indexes;
    static XplMutex indexesLock;
};


#endif
