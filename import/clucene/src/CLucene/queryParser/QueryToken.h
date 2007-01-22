/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_queryParser_QueryToken_
#define _lucene_queryParser_QueryToken_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "QueryParserConstants.h"

CL_NS_DEF(queryParser)

	// Token class that used by QueryParser.
	class QueryToken:LUCENE_BASE
	{
	public:
#ifndef LUCENE_HIDE_INTERNAL
#ifdef LUCENE_TOKEN_WORD_LENGTH
      TCHAR Value[LUCENE_TOKEN_WORD_LENGTH+1];
#else
		//Internal constant.
		const TCHAR* Value;
#endif

		//Internal constant.
		const int32_t Start;
		//Internal constant.
		int32_t End;
		//Internal constant.
		const QueryTokenTypes Type;
#endif

		// Initializes a new instance of the Token class LUCENE_EXPORT.
		QueryToken(const TCHAR* value, const int32_t start, const int32_t end, const QueryTokenTypes type);

		~QueryToken();

		// Initializes a new instance of the Token class LUCENE_EXPORT.
		QueryToken(const TCHAR* value, const QueryTokenTypes type);

		// Initializes a new instance of the Token class LUCENE_EXPORT.
		QueryToken(const QueryTokenTypes type);

		// Returns a string representation of the Token.
		//public override string toString();
	};
CL_NS_END
#endif
