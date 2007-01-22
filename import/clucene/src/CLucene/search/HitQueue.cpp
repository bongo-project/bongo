/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "HitQueue.h"

#include "CLucene/util/PriorityQueue.h"
#include "ScoreDoc.h"

CL_NS_DEF(search)
	

	HitQueue::HitQueue(const int32_t size) {
		initialize(size,true);
	}
	HitQueue::~HitQueue(){
	}

	bool HitQueue::lessThan(ScoreDoc* hitA, ScoreDoc* hitB) {
		//ScoreDoc* hitA = (ScoreDoc*)a;
		//ScoreDoc* hitB = (ScoreDoc*)b;
		if (hitA->score == hitB->score)
			return hitA->doc > hitB->doc; 
		else
			return hitA->score < hitB->score;
	}
CL_NS_END
