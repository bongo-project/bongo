/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_queryParser_QueryParserBase_
#define _lucene_queryParser_QueryParserBase_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/search/BooleanClause.h"
#include "CLucene/util/VoidList.h"
#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/analysis/Analyzers.h"
#include "CLucene/index/Term.h"
#include "QueryToken.h"



CL_NS_DEF(queryParser)

	// Contains routines that used by QueryParser.
	class QueryParserBase:LUCENE_BASE
	{
    protected:

		/* The actual operator the parser uses to combine query terms */
		int oper;

		bool lowercaseExpandedTerms;

    public:
    	LUCENE_STATIC_CONSTANT(int,  CONJ_NONE=0);
		LUCENE_STATIC_CONSTANT(int,  CONJ_AND=1);
		LUCENE_STATIC_CONSTANT(int,  CONJ_OR=2);
		
		LUCENE_STATIC_CONSTANT(int,  OR_OPERATOR=0);
		LUCENE_STATIC_CONSTANT(int,  AND_OPERATOR=1);
		
		LUCENE_STATIC_CONSTANT(int,  MOD_NONE=0);
		LUCENE_STATIC_CONSTANT(int,  MOD_NOT=10);
		LUCENE_STATIC_CONSTANT(int,  MOD_REQ=11);

		int32_t PhraseSlop; //0

		QueryParserBase();
		~QueryParserBase();

		//Sets the the flag lowercaseExpandedTerms
        void setLowercaseExpandedTerms(bool LowercaseExpandedTerms);

        //Returns the value of lowercaseExpandedTerms
        bool getLowercaseExpandedTerms();

		static void throwParserException(const TCHAR* message, TCHAR ch, int32_t col, int32_t line );

		void setOperator(int oper);
		int getOperator();

    protected:
        //todo: we should make these virtual so that users can override the QueryParser
        //and create their own custom implementations...
        
		// Adds the next parsed clause.
		void AddClause(CL_NS(util)::CLVector<CL_NS(search)::BooleanClause*>* clauses, int32_t conj, int32_t mods, CL_NS(search)::Query* q);

		// Returns a query for the specified field.
		CL_NS(search)::Query* GetFieldQuery(const TCHAR* field, CL_NS(analysis)::Analyzer* analyzer, const TCHAR* queryText);

		// Returns a range query.
		CL_NS(search)::Query* GetRangeQuery(const TCHAR* field, CL_NS(analysis)::Analyzer* analyzer, const TCHAR* queryText, bool inclusive);

        //Analyzes the expanded term termStr with the StandardFilter and the LowerCaseFilter.
        TCHAR* AnalyzeExpandedTerm(const TCHAR* field,const TCHAR* termStr);
	

#ifndef NO_PREFIX_QUERY
        //Factory method for generating a query. Called when parser parses 
	    //an input term token that uses prefix notation; that is, 
	    //contains a single '*' wildcard character as its last character. 
        //Since this is a special case of generic wildcard term, 
        //and such a query can be optimized easily, this usually results in a different 
        //query object.
        //
        //Depending on settings, a prefix term may be lower-cased automatically. 
        //It will not go through the default Analyzer, however, since normal Analyzers are 
        //unlikely to work properly with wildcard templates. Can be overridden by extending 
        //classes, to provide custom handling for wild card queries, which may be necessary 
        //due to missing analyzer calls.
	    CL_NS(search)::Query* GetPrefixQuery(const TCHAR* field,const TCHAR* termStr, bool lowercaseWildcardTerms);
#endif	
#ifndef NO_FUZZY_QUERY
		//Factory method for generating a query (similar to getPrefixQuery}). Called when parser parses
        //an input term token that has the fuzzy suffix (~) appended.
        CL_NS(search)::Query* GetFuzzyQuery(const TCHAR* field,const TCHAR* termStr, bool lowercaseWildcardTerms);
#endif
		
		/**
		* Returns a String where the escape char has been
		* removed, or kept only once if there was a double escape.
		*/
		TCHAR* discardEscapeChar(const TCHAR* token);
	};
CL_NS_END
#endif
