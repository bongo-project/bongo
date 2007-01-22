/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef MultiFieldQueryParser_H
#define MultiFieldQueryParser_H

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/search/SearchHeader.h"
#include "QueryParser.h"


CL_NS_DEF(queryParser)
	
#define queryParser_NORMAL_FIELD     0
#define queryParser_REQUIRED_FIELD   1
#define queryParser_PROHIBITED_FIELD 2
    /**
     * A QueryParser which constructs queries to search multiple fields.
     *
     */
    class MultiFieldQueryParser: public QueryParser
    {
    public:
    
        /*MultiFieldQueryParser(QueryParserTokenManager tm)
        {
            super(tm);
        }
    
        MultiFieldQueryParser(CL_NS(util)::Reader* stream):
            QueryParser(stream)
        {
        }*/
    
		MultiFieldQueryParser(TCHAR* f, CL_NS(analysis)::Analyzer* a):
        QueryParser(f,a)
        {
        }
    
        /**
         * <p>
         * Parses a query which searches on the fields specified.
         * <p>
         * If x fields are specified, this effectively constructs:
         * <pre>
         * <code>
         * (field1:query) (field2:query) (field3:query)...(fieldx:query)
         * </code>
         * </pre>
         *
         * @param query Query string to parse
         * @param fields Fields to search on
         * @param analyzer Analyzer to use
         * @throws ParserException if query parsing fails
         * @throws TokenMgrError if query parsing fails
         */
		static CL_NS(search)::Query* parse(const TCHAR* query, const TCHAR** fields, CL_NS(analysis)::Analyzer* analyzer);
    
        /**
         * <p>
         * Parses a query, searching on the fields specified.
         * Use this if you need to specify certain fields as required,
         * and others as prohibited.
         * <p><pre>
         * Usage:
         * <code>
         * String[] fields = {"filename", "contents", "description"};
         * int32_t[] flags = {MultiFieldQueryParser.NORMAL FIELD,
         *                MultiFieldQueryParser.REQUIRED FIELD,
         *                MultiFieldQueryParser.PROHIBITED FIELD,};
         * parse(query, fields, flags, analyzer);
         * </code>
         * </pre>
         *<p>
         * The code above would construct a query:
         * <pre>
         * <code>
         * (filename:query) +(contents:query) -(description:query)
         * </code>
         * </pre>
         *
         * @param query Query string to parse
         * @param fields Fields to search on
         * @param flags Flags describing the fields
         * @param analyzer Analyzer to use
         * @throws ParserException if query parsing fails
         * @throws TokenMgrError if query parsing fails
         */
		static CL_NS(search)::Query* parse(const TCHAR* query, const TCHAR** fields, const uint8_t* flags, CL_NS(analysis)::Analyzer* analyzer);
    };
CL_NS_END
#endif
