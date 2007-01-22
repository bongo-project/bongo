/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_FieldDocSortedHitQueue_
#define _lucene_search_FieldDocSortedHitQueue_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "ScoreDoc.h"
#include "FieldCache.h"
#include "Sort.h"
#include "FieldDoc.h"
#include "SearchHeader.h"
#include "FieldCacheImpl.h"
#include "CLucene/util/PriorityQueue.h"
#include "Compare.h"

CL_NS_DEF(search)

/**
 * Expert: Collects sorted results from Searchable's and collates them.
 * The elements put into this queue must be of type FieldDoc.
 *
 */
class FieldDocSortedHitQueue: 
	public CL_NS(util)::PriorityQueue<FieldDoc*,CL_NS(util)::Deletor::Object<ScoreDoc> > 
{
private:
    DEFINE_MUTEX(THIS_LOCK)

	// this cannot contain AUTO fields - any AUTO fields should
	// have been resolved by the time this class is used.
	SortField** fields; //volatile 
	int32_t fieldsLen;
	
	void _countsize(){
		fieldsLen=0;
		while(fields[fieldsLen]!=NULL)
			fieldsLen++;
	}

	// used in the case where the fields are sorted by locale
	// based strings
	//todo: not implemented in clucene because locales has not been implemented
	//Collator[] collators; //volatile 

public:
	/**
	 * Creates a hit queue sorted by the given list of fields.
	 * @param fields Field names, in priority order (highest priority first).
	 * @param size  The number of hits to retain.  Must be greater than zero.
	 */
	FieldDocSortedHitQueue (SortField** fields, int32_t size) {
		this->fields = fields;
		_countsize();
		//this->collators = hasCollators (fields);
		initialize (size,true);
	}

    ~FieldDocSortedHitQueue();


	/**
	 * Allows redefinition of sort fields if they are <code>NULL</code>.
	 * This is to handle the case using ParallelMultiSearcher where the
	 * original list contains AUTO and we don't know the actual sort
	 * type until the values come back.  The fields can only be set once.
	 * This method is thread safe.
	 * @param fields
	 */
	void setFields (SortField** fields);


	/** Returns the fields being used to sort. */
	SortField** getFields() {
		return fields;
	}


	/** Returns an array of collators, possibly <code>NULL</code>.  The collators
	 * correspond to any SortFields which were given a specific locale.
	 * @param fields Array of sort fields.
	 * @return Array, possibly <code>NULL</code>.
	 
	private Collator[] hasCollators (SortField[] fields) {
		if (fields == NULL) return NULL;
		Collator[] ret = new Collator[fields.length];
		for (int32_t i=0; i<fields.length; ++i) {
			Locale locale = fields[i].getLocale();
			if (locale != NULL)
				ret[i] = Collator.getInstance (locale);
		}
		return ret;
	}*/

protected:
	/**
	 * Returns whether <code>a</code> is less relevant than <code>b</code>.
	 * @param a ScoreDoc
	 * @param b ScoreDoc
	 * @return <code>true</code> if document <code>a</code> should be sorted after document <code>b</code>.
	 */
	bool lessThan (FieldDoc* docA, FieldDoc* docB);
};


CL_NS_END
#endif

