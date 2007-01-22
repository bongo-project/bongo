/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_SegmentMergeInfo_
#define _lucene_index_SegmentMergeInfo_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/BitVector.h"
#include "SegmentTermEnum.h"
#include "SegmentHeader.h"

CL_NS_DEF(index)
	class SegmentMergeInfo:LUCENE_BASE {
	public:
		TermEnum* termEnum;
		Term* term;
		int32_t base;
		int32_t* docMap;				  // maps around deleted docs
		IndexReader* reader;
		TermPositions* postings;
         
		//Constructor
		SegmentMergeInfo(const int32_t b, TermEnum* te, IndexReader* r);

		//Destructor
		~SegmentMergeInfo();

		//Moves the current term of the enumeration termEnum to the next and term
	    //points to this new current term
		bool next();

		//Closes the the resources
		void close();
 
	};
CL_NS_END
#endif

