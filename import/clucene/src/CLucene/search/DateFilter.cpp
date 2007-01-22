/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "DateFilter.h"

CL_NS_USE(index)
CL_NS_USE(util)
CL_NS_USE(document)
CL_NS_DEF(search)

  DateFilter::~DateFilter(){
    _CLDELETE_CARRAY( start );
    _CLDELETE_CARRAY( field );
    _CLDELETE_CARRAY( end );
  }
  
  DateFilter::DateFilter(const DateFilter& copy):
    start ( STRDUP_TtoT(copy.start) ),
    end ( STRDUP_TtoT(copy.end) )
  {
     field = STRDUP_TtoT(copy.field);
  }

  /** Constructs a filter for field <code>f</code> matching times between
    <code>from</code> and <code>to</code>. */
  DateFilter::DateFilter(const TCHAR* f, int64_t from, int64_t to):
    start ( DateField::timeToString(from) ),
    end ( DateField::timeToString(to) )
  {
     field = STRDUP_TtoT(f);
  }

  /** Constructs a filter for field <code>f</code> matching times before
    <code>time</code>. */
  DateFilter* DateFilter::Before(const TCHAR* field, int64_t time) {
    return _CLNEW DateFilter(field, 0,time);
  }

  /** Constructs a filter for field <code>f</code> matching times after
    <code>time</code>. */
  DateFilter* DateFilter::After(const TCHAR* field, int64_t time) {
    return _CLNEW DateFilter(field,time, DATEFIELD_DATE_MAX );
  }

  /** Returns a BitSet with true for documents which should be permitted in
    search results, and false for those that should not. */
  BitSet* DateFilter::bits(IndexReader* reader) {
    BitSet* bts = _CLNEW BitSet(reader->maxDoc());

    Term* t = _CLNEW Term(field, start,false); //todo: was false
    TermEnum* enumerator = reader->terms(t);
    _CLDECDELETE(t);

    if (enumerator->term(false) == NULL){
      _CLDELETE(enumerator);
      return bts;
    }
    TermDocs* termDocs = reader->termDocs();

    try {
      Term* stop = _CLNEW Term(field, end, false);
      while (enumerator->term(false)->compareTo(stop) <= 0) {
        termDocs->seek(enumerator->term(false));
        while (termDocs->next()) {
          bts->set(termDocs->doc());
        }
        if (!enumerator->next()) {
          break;
        }
      }
      _CLDECDELETE(stop);
    } _CLFINALLY (
        termDocs->close();
        _CLVDELETE(termDocs); //todo: should be _delete
        enumerator->close();
        _CLDELETE(enumerator);
    );
    return bts;
  }
  
  Filter* DateFilter::clone() const{
  	return _CLNEW DateFilter(*this);	
  }

  TCHAR* DateFilter::toString(){
    size_t len = _tcslen(field) + _tcslen(start) + _tcslen(end) + 8;
	TCHAR* ret = _CL_NEWARRAY(TCHAR,len);
	ret[0]=0;
	_sntprintf(ret,len,_T("%s: [%s-%s]"),field,start,end);
	return ret;
  }
CL_NS_END
