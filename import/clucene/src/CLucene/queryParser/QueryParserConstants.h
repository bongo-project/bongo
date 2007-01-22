/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_queryParser_QueryParserConstants_
#define _lucene_queryParser_QueryParserConstants_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

CL_NS_DEF(queryParser)

	enum QueryTokenTypes
	{
		AND_,
		OR,
		NOT,
		PLUS,
		MINUS,
		LPAREN,
		RPAREN,
		COLON,
		CARAT,
		QUOTED,
		TERM,
		SLOP,
#ifndef NO_FUZZY_QUERY
		FUZZY,
#endif
#ifndef NO_PREFIX_QUERY
		PREFIXTERM,
#endif
#ifndef NO_WILDCARD_QUERY
		WILDTERM,
#endif
#ifndef NO_RANGE_QUERY
		RANGEIN,
		RANGEEX,
#endif
		NUMBER,
		EOF_
	};

CL_NS_END
#endif
