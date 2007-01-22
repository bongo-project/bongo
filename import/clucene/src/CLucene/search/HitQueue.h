/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_HitQueue_
#define _lucene_search_HitQueue_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/PriorityQueue.h"
#include "ScoreDoc.h"

CL_NS_DEF(search)
	class HitQueue: public CL_NS(util)::PriorityQueue<ScoreDoc*,CL_NS(util)::Deletor::Object<ScoreDoc> > {
	public:
		HitQueue(const int32_t size);
		~HitQueue();

	protected:
		inline bool lessThan(ScoreDoc* hitA, ScoreDoc* hitB);
	};
CL_NS_END
#endif
