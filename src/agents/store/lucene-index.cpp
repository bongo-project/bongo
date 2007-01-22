#include <config.h>

extern "C" {
#include "stored.h"
}

#include "lucene-index.h"


// Keyword analyzer
class KeywordAnalyzer : public Analyzer 
{    
public :
    TokenStream *tokenStream (const TCHAR *fieldName, Reader *reader);
    ~KeywordAnalyzer() {}
};

class KeywordTokenizer : public Tokenizer
{
private :
    TCHAR buffer[LUCENE_MAX_WORD_LEN + 1];
    int32_t offset, bufferIndex, dataLen;
public :
    KeywordTokenizer(Reader *in);
    virtual ~KeywordTokenizer() { }

    bool next(Token *token);
};


TokenStream *
KeywordAnalyzer::tokenStream(const TCHAR *fieldName, Reader *reader)
{
    return _CLNEW KeywordTokenizer(reader);
}


KeywordTokenizer::KeywordTokenizer(Reader *reader)  : offset(0),
                                                      bufferIndex(0),
                                                      dataLen(0)

{
    input = reader;
}

bool
KeywordTokenizer::next(Token *token) 
{
    int32_t length = 0;
    int32_t start = offset;
    const TCHAR *ioBuffer;
    
    dataLen = input->read(ioBuffer, LUCENE_IO_BUFFER_SIZE);
    if (dataLen <= 0) {
        return false;
    }
    token->set(ioBuffer, 0, dataLen);
    return true;
#if 0
    while (true) {
        TCHAR c;
        offset++;
        if (bufferIndex >= dataLen) {
            dataLen = input->read(ioBuffer, LUCENE_IO_BUFFER_SIZE);
            ioBuffer[dataLen] = 0;
            bufferIndex = 0;
        }

        if (dataLen <= 0 ) {
            if (length > 0) {
                break;
            } else {
                return false;
            }
        } else {
            c = ioBuffer[bufferIndex++];
        }

        if (length == 0) {
            start = offset - 1;
        }
            
        buffer[length++] = c;                
        if (length == LUCENE_MAX_WORD_LEN) {
            break;
        };
    }

    buffer[length] = 0;
    
    token->set (buffer, start, start + length);
    return true;        
#endif
}



LuceneIndex::LuceneIndex() : reader(NULL), 
                             writer(NULL), 
                             analyzer(NULL), 
                             queryParser(NULL),
                             searcher(NULL),
                             basePath(NULL)
{
}

LuceneIndex::~LuceneIndex() 
{
    if (basePath) {
        MemFree(basePath);
    }

    if (analyzer) {
        delete analyzer;
    }

    if (writer) {
        writer->close();
        delete writer;
    }    

    if (searcher) {
        searcher->close();
        delete searcher;
    }

    if (reader) {
        reader->close();
        delete reader;
    }
}

Analyzer *
LuceneIndex::GetAnalyzer()
{
    PerFieldAnalyzerWrapper *perfield = new PerFieldAnalyzerWrapper(new SimpleAnalyzer());
    perfield->addAnalyzer(_T("nmap.summary.listid"), _CLNEW(KeywordAnalyzer));
    perfield->addAnalyzer(_T("nmap.summary.from"), _CLNEW(KeywordAnalyzer));
    perfield->addAnalyzer(_T("nmap.summary.to"), _CLNEW(KeywordAnalyzer));
    perfield->addAnalyzer(_T("nmap.mail.attachmentmimetype"), _CLNEW(KeywordAnalyzer));
    return perfield;
}

int
LuceneIndex::Init(const char *basePath) 
{
    this->basePath = MemStrdup(basePath);    
    analyzer = GetAnalyzer();
    directory = GetDirectory();
    
    if (!directory) {
        return 1;
    }

    return 0;
}

Directory * 
LuceneIndex::GetDirectory()
{
    char segmentsFile[XPL_MAX_PATH + 1];

    snprintf(segmentsFile, sizeof(segmentsFile), "%s/segments", basePath);

    if (access(segmentsFile, 0) == 0) {
        // This is the only process managing IndexReaders, so it's
        // safe to break the lock here
        Directory *dir = FSDirectory::getDirectory(basePath, false);
        if (IndexReader::isLocked(dir)) {
            printf("bongostore: Clearing stale index lock\n");
            IndexReader::unlock(dir);
        }
        
        return dir;
    } else {
        XplMakeDir(basePath);

        /* create a fresh index with a dummy document. */

        Directory *ret = FSDirectory::getDirectory(basePath, true);
        IndexWriter *writer = new IndexWriter(ret, NULL, true);
        Document *doc = new Document();
        doc->add(*Field::Keyword(_T("nmap.dummy"), L"@dummy@"));
        doc->add(*Field::Keyword(_T("nmap.type"), L"dummy"));
        doc->add(*Field::Keyword(_T("nmap.sort"), L"dummy"));
        
        writer->addDocument(doc);
        writer->close();
        delete doc;
        delete writer;
        
        return ret;        
    }    
}

IndexWriter *
LuceneIndex::GetIndexWriter() 
{
    if (reader) {
        reader->close();
        delete reader;
        reader = NULL;
    }

    if (writer) {
        return writer;
    }
    
    writer = new IndexWriter(directory, analyzer, false);
    writer->setMergeFactor(100);
    writer->setMaxMergeDocs(1000);
    
    return writer;
}

IndexReader *
LuceneIndex::GetIndexReader()
{
    if (writer) {
        writer->close();
        delete writer;
        writer = NULL;
    }

    if (reader) {
        return reader;
    }
    
    reader = IndexReader::open(directory);
    
    return reader;
}

IndexSearcher *
LuceneIndex::GetIndexSearcher()
{

    if (searcher) {
        return searcher;
    }
    
    searcher = new IndexSearcher(GetIndexReader());
    
    return searcher;
}


QueryParser *
LuceneIndex::GetQueryParser()
{

    if (queryParser) {
        return queryParser;
    }
    
    queryParser = new QueryParser(_T("everything"), analyzer);
    queryParser->setOperator(lucene::queryParser::QueryParserBase::AND_OPERATOR);
    
    return queryParser;
}


