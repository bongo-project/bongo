/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "MultiFieldQueryParser.h"
#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/search/BooleanQuery.h"
#include "CLucene/search/SearchHeader.h"
#include "QueryParser.h"

CL_NS_USE(index)
CL_NS_USE(util)
CL_NS_USE(search)
CL_NS_USE(analysis)

CL_NS_DEF(queryParser)

    //static 
    Query* MultiFieldQueryParser::parse(const TCHAR* query, const TCHAR** fields, Analyzer* analyzer)
    {
        BooleanQuery* bQuery = _CLNEW BooleanQuery();
        int32_t i = 0;
        while ( fields[i] != NULL ){
			   Query* q = QueryParser::parse(query, fields[i], analyzer);
            bQuery->add(q, true, false, false);

            i++;
        }
        return bQuery;
    }

    //static 
    Query* MultiFieldQueryParser::parse(const TCHAR* query, const TCHAR** fields, const uint8_t* flags, Analyzer* analyzer)
    {
        BooleanQuery* bQuery = _CLNEW BooleanQuery();
        int32_t i = 0;
        while ( fields[i] != NULL )
        {
			Query* q = QueryParser::parse(query, fields[i], analyzer);
            uint8_t flag = flags[i];
            switch (flag)
            {
                case queryParser_REQUIRED_FIELD:
                    bQuery->add(q, true, true, false);
                    break;
                case queryParser_PROHIBITED_FIELD:
                    bQuery->add(q, true, false, true);
                    break;
                default:
                    bQuery->add(q, true, false, false);
                    break;
            }

            i++;
        }
        return bQuery;
    }
CL_NS_END
