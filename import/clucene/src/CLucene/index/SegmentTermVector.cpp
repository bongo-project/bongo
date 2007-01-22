/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "TermVector.h"
#include "CLucene/util/StringBuffer.h"

CL_NS_USE(util)
CL_NS_DEF(index)

	//note: termFreqs must be the same length as terms
  SegmentTermVector::SegmentTermVector(const TCHAR* field, const TCHAR** terms, int32_t* termFreqs) {
    this->field = STRDUP_TtoT(field);
    this->terms = terms;
	this->termsLen = -1; //lazily get the size of the terms
    this->termFreqs = termFreqs;
  }

  SegmentTermVector::~SegmentTermVector(){
      _CLDELETE_CARRAY(field);
      _CLDELETE_CARRAY_ALL(terms);
      _CLDELETE_ARRAY(termFreqs);
  }

  const TCHAR* SegmentTermVector::getField() {
    return field;
  }

  TCHAR* SegmentTermVector::toString() {
    StringBuffer sb;
    sb.appendChar('{');
    sb.append(field);
	sb.append(_T(": "));

	int32_t i=0;
	while ( terms[i] != NULL ){
      if (i>0) 
		  sb.append(_T(", "));
      sb.append(terms[i]);
	  sb.appendChar('/');

	  sb.appendInt(termFreqs[i]);
    }
    sb.appendChar('}');
    return sb.toString();
  }

  int32_t SegmentTermVector::size() {
    if ( terms == NULL )
		return 0;

		if ( termsLen == -1 ){
			termsLen=0;
			while ( terms[termsLen] != 0 )
				termsLen++;
		}
    return termsLen;
  }

  const TCHAR** SegmentTermVector::getTerms() {
    return terms;
  }

  const int32_t* SegmentTermVector::getTermFrequencies() {
    return termFreqs;
  }

  int32_t SegmentTermVector::binarySearch(const TCHAR** a, const int32_t arraylen, const TCHAR* key) const
	{
		int32_t low = 0;
		int32_t hi = arraylen - 1;
		int32_t mid = 0;
		while (low <= hi)
		{
			mid = (low + hi) >> 1;

			int32_t c = _tcscmp(a[mid],key);
			if (c==0)
				return mid;
			else if (c > 0)
				hi = mid - 1;
			else // This gets the insertion point right on the last loop.
				low = ++mid;
		}
		return -mid - 1;
	}
  int32_t SegmentTermVector::indexOf(const TCHAR* termText) {
		int32_t res = binarySearch(terms, size(), termText);
    return res >= 0 ? res : -1;
  }

  const int32_t* SegmentTermVector::indexesOf(const TCHAR** termNumbers, int32_t start, int32_t len) {
    // TODO: there must be a more efficient way of doing this.
    //       At least, we could advance the lower bound of the terms array
    //       as we find valid indexes. Also, it might be possible to leverage
    //       this even more by starting in the middle of the termNumbers array
    //       and thus dividing the terms array maybe in half with each found index.
    int32_t* res = _CL_NEWARRAY(int32_t,len);

    for (int32_t i=0; i<len; i++) {
      res[i] = indexOf(termNumbers[i]);
    }

    return res;
  }

CL_NS_END

