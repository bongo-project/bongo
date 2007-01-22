/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "TermScorer.h"

#include "CLucene/index/Terms.h"
#include "TermQuery.h"

CL_NS_USE(index)
CL_NS_DEF(search)

	//TermScorer takes TermDocs and delets it when TermScorer is cleaned up
	TermScorer::TermScorer(Weight* w, CL_NS(index)::TermDocs* td, 
		Similarity* similarity,uint8_t* _norms):
	    Scorer(similarity),
	    termDocs(td),
	    norms(_norms),
	    weight(w),
	    weightValue(w->getValue()),
	    pointer(0),
	    pointerMax(0),
	    _doc(0)
	{
		for (int32_t i = 0; i < LUCENE_SCORE_CACHE_SIZE; i++)
			scoreCache[i] = getSimilarity()->tf(i) * weightValue;

		/*pointerMax = termDocs->read(docs, freqs);	  // fill buffers

		if (pointerMax != 0)
			doc = docs[0];
		else {
			termDocs->close();				  // close stream
			doc = INT_MAX;			  // set to sentinel value
		}*/
	}

	TermScorer::~TermScorer(){
		_CLVDELETE(termDocs); //todo: not a clucene object... should be
	}
  bool TermScorer::next(){
    pointer++;
    if (pointer >= pointerMax) {
      pointerMax = termDocs->read(docs, freqs, 32);    // refill buffer
      if (pointerMax != 0) {
        pointer = 0;
      } else {
        termDocs->close();			  // close stream
        _doc = LUCENE_INT32_MAX_SHOULDBE;		  // set to sentinel value
        return false;
      }
    } 
    _doc = docs[pointer];
    return true;
  }

  bool TermScorer::skipTo(int32_t target) {
    // first scan in cache
    for (pointer++; pointer < pointerMax; pointer++) {
      if (docs[pointer] >= target) {
        _doc = docs[pointer];
        return true;
      }
    }

    // not found in cache, seek underlying stream
    bool result = termDocs->skipTo(target);
      if (result) {
         pointerMax = 1;
         pointer = 0;
         docs[pointer] = _doc = termDocs->doc();
         freqs[pointer] = termDocs->freq();
      } else {
         _doc = LUCENE_INT32_MAX_SHOULDBE;
      }
      return result;
  }

  Explanation* TermScorer::explain(int32_t doc) {
    TermQuery* query = (TermQuery*)weight->getQuery();
    Explanation* tfExplanation = _CLNEW Explanation();
    int32_t tf = 0;
    while (pointer < pointerMax) {
      if (docs[pointer] == doc)
        tf = freqs[pointer];
      pointer++;
    }
    if (tf == 0) {
      while (termDocs->next()) {
        if (termDocs->doc() == _doc) {
          tf = termDocs->freq();
        }
      }
    }
    termDocs->close();
    tfExplanation->setValue(getSimilarity()->tf(tf));

    TCHAR buf[LUCENE_SEARCH_EXPLANATION_DESC_LEN+1];
    _tcscpy(buf,_T("tf(termFreq("));
    Term* term = query->getTerm();
    TCHAR* tmp = term->toString();
    _CLDECDELETE(term);
    _tcscat(buf,tmp);
    _CLDELETE_CARRAY(tmp);
    _tcscat(buf,_T(")="));
    
    TCHAR bufn[12];
    _i64tot(tf,bufn,10);
    _tcscat(buf,bufn);

    _tcscat(buf,_T(")"));
    tfExplanation->setDescription(buf);
    
    return tfExplanation;
  }

  TCHAR* TermScorer::toString() { 
     TCHAR* wb = weight->toString();
     int32_t rl = _tcslen(wb) + 9; //9=_tcslen("scorer("  ")") + 1
     TCHAR* ret = _CL_NEWARRAY(TCHAR,rl);
     _tcscpy(ret,_T("scorer("));
     _tcscat(ret,wb);
     _tcscat(ret,_T(")"));
     _CLDELETE_ARRAY(wb);
     return ret;
  }

  float_t TermScorer::score() {
	 int32_t f = freqs[pointer];
    float_t raw =                                   // compute tf(f)*weight
      f < LUCENE_SCORE_CACHE_SIZE			  // check cache
      ? scoreCache[f]                             // cache hit
      : getSimilarity()->tf(f) * weightValue;        // cache miss

      return raw * Similarity::decodeNorm(norms[_doc]); // normalize for field
  }
	
CL_NS_END
