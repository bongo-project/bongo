/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "test.h"
#include "../util/English.h"

IndexSearcher* tv_searcher;
RAMDirectory tv_directory;

void testTermPositionVectors(CuTest *tc) {
    CLUCENE_ASSERT(tv_searcher!=NULL);

    Term* term = _CLNEW Term(_T("field"), _T("fifty"));
    TermQuery query(term);
    _CLDECDELETE(term);
    try {
      Hits* hits = tv_searcher->search(&query);
      CuAssert (tc,_T("hits.length != 100"), 100 == hits->length());
      
      for (int32_t i = 0; i < hits->length(); i++)
      {
        TermFreqVector** vector = tv_searcher->getReader()->getTermFreqVectors(hits->id(i));
        CLUCENE_ASSERT(vector != NULL);

        int32_t vector_len = 0;
        while(vector[vector_len]!=NULL){
            _CLDELETE(vector[vector_len]);
            vector_len++;
        }

        CLUCENE_ASSERT(vector_len== 1);
        _CLDELETE_ARRAY(vector);
        //assertTrue();
      }

      _CLDELETE(hits);
    } catch (CLuceneError& e) {
        if ( e.number() == CL_ERR_IO )
            CuAssert(tc, _T("IO Error"),false);
      
        throw e;
    }
}
void testTermVectors(CuTest *tc) {
    CLUCENE_ASSERT(tv_searcher!=NULL);

    Term* term = _CLNEW Term(_T("field"), _T("seventy"));
    TermQuery query(term);
    _CLDECDELETE(term);

    try {
      Hits* hits = tv_searcher->search(&query);
      CuAssertIntEquals(tc,_T("hits!=100"), 100, hits->length());
      
      for (int32_t i = 0; i < hits->length(); i++)
      {
        TermFreqVector** vector = tv_searcher->getReader()->getTermFreqVectors(hits->id(i));
        CLUCENE_ASSERT(vector != NULL);

        int32_t vector_len=0;
        while(vector[vector_len] != NULL ){
            _CLDELETE(vector[vector_len]);
            vector_len++;
        }
        CLUCENE_ASSERT(vector_len == 1);
        
        _CLDELETE_ARRAY(vector);
      }
      TermFreqVector** vector = tv_searcher->getReader()->getTermFreqVectors(hits->id(50));

      Explanation* expl = tv_searcher->explain(&query, hits->id(50));
      TCHAR* tmp = expl->toString();
      _CLDELETE(expl);
      //CuMessage(tc,_T("Explain: %s"), tmp);
      _CLDELETE_CARRAY(tmp);
      //CuMessage(tc,_T("Vector: %s"), vector[0]->toString());

      int32_t vector_len=0;
      while(vector[vector_len] != NULL ){
            _CLDELETE(vector[vector_len]);
            vector_len++;
      }
      _CLDELETE_ARRAY(vector);

      _CLDELETE(hits);

    } catch (CLuceneError& e) {
        if ( e.number() == CL_ERR_IO )
            CuAssert(tc,_T("IO Exception"),false);
        else
            throw e;
    }
}

void testTVCleanup(CuTest *tc) {
    _CLDELETE(tv_searcher);
	tv_directory.close();
}
void testTVSetup(CuTest *tc) {
    SimpleAnalyzer a;
    IndexWriter writer(&tv_directory, &a, true);
    writer.setUseCompoundFile(false);

    TCHAR buf[200];
    for (int32_t i = 0; i < 1000; i++) {
      Document doc;
      English::IntToEnglish(i,buf,200);

      doc.add(*Field::Text(_T("field"), buf, true));
      writer.addDocument(&doc);
    }
    writer.close();
    tv_searcher = _CLNEW IndexSearcher(&tv_directory);
}


void setupDoc(Document& doc, TCHAR* text)
{
    doc.add(*Field::Text(_T("field"), text, true));
    //System.out.println("Document: " + doc);
}
void testKnownSetOfDocuments(CuTest *tc) {
    const TCHAR* termArray[12] = {_T("eating"), _T("chocolate"), _T("in"), _T("a"), 
        _T("computer"), _T("lab"), _T("grows"), 
        _T("old"), _T("colored"), _T("with"), _T("an"), NULL};
    TCHAR* test1 = _T("eating chocolate in a computer lab"); //6 terms
    TCHAR* test2 = _T("computer in a computer lab"); //5 terms
    TCHAR* test3 = _T("a chocolate lab grows old"); //5 terms
    TCHAR* test4 = _T("eating chocolate with a chocolate lab in an old chocolate colored computer lab"); //13 terms
    
    typedef CL_NS(util)::CLHashMap<const TCHAR*,int32_t,CL_NS(util)::Compare::TChar,
        CL_NS(util)::Deletor::Dummy,CL_NS(util)::Deletor::DummyInt32> test4MapType;
    test4MapType test4Map;
    test4Map.put(_T("chocolate"), 3);
    test4Map.put(_T("lab"), 2);
    test4Map.put(_T("eating"), 1);
    test4Map.put(_T("computer"), 1);
    test4Map.put(_T("with"), 1);
    test4Map.put(_T("a"), 1);
    test4Map.put(_T("colored"), 1);
    test4Map.put(_T("in"), 1);
    test4Map.put(_T("an"), 1);
    test4Map.put(_T("computer"), 1);
    test4Map.put(_T("old"), 1);
    
    Document testDoc1;
    setupDoc(testDoc1, test1);
    Document testDoc2;
    setupDoc(testDoc2, test2);
    Document testDoc3;
    setupDoc(testDoc3, test3);
    Document testDoc4;
    setupDoc(testDoc4, test4);
        
    RAMDirectory dir;
    
    try {
      SimpleAnalyzer a;
      IndexWriter writer(&dir, &a, true);

      writer.addDocument(&testDoc1);
      writer.addDocument(&testDoc2);
      writer.addDocument(&testDoc3);
      writer.addDocument(&testDoc4);
      writer.close();

      IndexSearcher knownSearcher(&dir);
      TermEnum* termEnum = knownSearcher.getReader()->terms();
      TermDocs* termDocs = knownSearcher.getReader()->termDocs();
      //System.out.println("Terms: " + termEnum.size() + " Orig Len: " + termArray.length);
      
      Similarity* sim = knownSearcher.getSimilarity();
      while (termEnum->next() == true)
      {
        Term* term = termEnum->term(true);
        //System.out.println("Term: " + term);
        termDocs->seek(term);

        while (termDocs->next())
        {
          int32_t docId = termDocs->doc();
          int32_t freq = termDocs->freq();
          //System.out.println("Doc Id: " + docId + " freq " + freq);
          TermFreqVector* vector = knownSearcher.getReader()->getTermFreqVector(docId, _T("field"));
          float_t tf = sim->tf(freq);
          float_t idf = sim->idf(term, &knownSearcher);
          //float_t qNorm = sim.queryNorm()
          
          int termsCount=0;
          const TCHAR** terms = vector->getTerms();
          CLUCENE_ASSERT(vector != NULL);
          while ( terms[termsCount] != NULL )
              termsCount++;

          //This is fine since we don't have stop words
          float_t lNorm = sim->lengthNorm(_T("field"), termsCount);
          //float_t coord = sim.coord()
          //System.out.println("TF: " + tf + " IDF: " + idf + " LenNorm: " + lNorm);
          const TCHAR** vTerms = vector->getTerms();
          const int32_t* freqs = vector->getTermFrequencies();
          int32_t i=0; 
          while ( vTerms[i] != NULL )
          {
            if ( _tcscmp(term->text(), vTerms[i]) == 0 )
            {
              CLUCENE_ASSERT(freqs[i] == freq);
            }
            i++;
          }
          
          _CLDELETE(vector);
        }
        _CLDECDELETE(term);
        //System.out.println("--------");
      }
      _CLDELETE(termEnum);
      _CLDELETE(termDocs);


      Term* tqTerm = _CLNEW Term(_T("field"), _T("chocolate"));
      TermQuery query(tqTerm);
      _CLDECDELETE(tqTerm);

      Hits* hits = knownSearcher.search(&query);
      //doc 3 should be the first hit b/c it is the shortest match
      CLUCENE_ASSERT(hits->length() == 3);
      float_t score = hits->score(0);
      
      TCHAR* tmp1; TCHAR* tmp2;
#define __CLASSERT(x1,x2) tmp1=x1.toString(); tmp2=x2.toString(); CuAssertStrEquals(tc, _T(#x1) _T(".toString()!=doc to string"), tmp1,tmp2); _CLDELETE_CARRAY(tmp1);_CLDELETE_CARRAY(tmp2);
      __CLASSERT(testDoc3, hits->doc(0) );
      __CLASSERT(testDoc4, hits->doc(1) );
      __CLASSERT(testDoc1, hits->doc(2) );

      TermFreqVector* vector = knownSearcher.getReader()->getTermFreqVector(hits->id(1), _T("field"));
      CLUCENE_ASSERT(vector != NULL);
      //System.out.println("Vector: " + vector);
      const TCHAR** terms = vector->getTerms();
      const int32_t* freqs = vector->getTermFrequencies();
      CLUCENE_ASSERT(terms != NULL);
      
      int termsLength = 0;
      while ( terms[termsLength] != NULL )
          termsLength++;
      CLUCENE_ASSERT(termsLength == 10);

      for (int32_t i = 0; i < termsLength; i++) {
        const TCHAR* term = terms[i];
        //System.out.println("Term: " + term);
        int32_t freq = freqs[i];
        CLUCENE_ASSERT( _tcsstr(test4,term) != NULL );
        test4MapType::iterator itr = test4Map.find(term);
        CLUCENE_ASSERT( itr != test4Map.end() );
        int32_t freqInt = itr->second;
        CLUCENE_ASSERT(freqInt == freq);        
      }
      _CLDELETE(vector);
      _CLDELETE(hits);
      knownSearcher.close();

    } catch (CLuceneError& e) {
      CuAssert(tc, e.twhat(),false);
    }
}

CuSuite *testtermvector(void)
{
    tv_searcher = NULL;
	CuSuite *suite = CuSuiteNew(_T("CLucene Term Vector Test"));
    SUITE_ADD_TEST(suite, testTVSetup);
    SUITE_ADD_TEST(suite, testKnownSetOfDocuments);
    SUITE_ADD_TEST(suite, testTermVectors);
    SUITE_ADD_TEST(suite, testTermPositionVectors);
    SUITE_ADD_TEST(suite, testTVCleanup);

    return suite; 
}
// EOF
