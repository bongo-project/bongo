/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_ScoreDoc_
#define _lucene_search_ScoreDoc_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

CL_NS_DEF(search)
/** Expert: Returned by low-level search implementations.
 * @see TopDocs */
	class ScoreDoc: LUCENE_BASE {
	public:
		/** Expert: The score of this document for the query. */
			float_t score;

		/** Expert: A hit document's number.
		* @see Searcher#doc(int32_t)
		*/
			int32_t doc;

		/** Expert: Constructs a ScoreDoc. */
		ScoreDoc(const int32_t d, const float_t s):
			score(s),
			doc(d)
		{
		}
		~ScoreDoc(){
		}
	};
CL_NS_END
#endif
