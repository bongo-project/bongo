/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_queryParser_Lexer_
#define _lucene_queryParser_Lexer_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "QueryParserConstants.h"

#include "CLucene/util/FastCharStream.h"
#include "CLucene/util/Reader.h"
#include "CLucene/util/StringBuffer.h"

#include "TokenList.h"

CL_NS_DEF(queryParser)

	// A simple Lexer that is used by QueryParser.
	class Lexer:LUCENE_BASE
	{
	private:
		CL_NS(util)::FastCharStream* reader;
		bool delSR;  //Indicates if the reader must be deleted or not

    public:
		// Initializes a new instance of the Lexer class with the specified
		// query to lex.
		Lexer(const TCHAR* query);

		// Initializes a new instance of the Lexer class with the specified
		// TextReader to lex.
		Lexer(CL_NS(util)::Reader* source);

		//Breaks the input stream onto the tokens list tokens
		void Lex(TokenList *tokens);
		
		~Lexer();

	private:
		QueryToken* GetNextToken();

		// Reads an integer number
		const TCHAR* ReadIntegerNumber(const TCHAR ch);

#ifndef NO_RANGE_QUERY
		// Reads an inclusive range like [some words]
		QueryToken* ReadInclusiveRange(const TCHAR prev);

		// Reads an exclusive range like {some words}
		QueryToken* ReadExclusiveRange(const TCHAR prev);
#endif

		// Reads quoted string like "something else"
		QueryToken* ReadQuoted(const TCHAR prev);

		QueryToken* ReadTerm(const TCHAR prev);


		const TCHAR* ReadEscape(const TCHAR prev);
	};
CL_NS_END
#endif
