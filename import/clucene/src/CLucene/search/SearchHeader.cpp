/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "SearchHeader.h"
#include "BooleanQuery.h"

CL_NS_USE(index)
CL_NS_DEF(search)

//static
Query* Query::mergeBooleanQueries(Query** queries) {
	//todo: was using hashset???
    CL_NS(util)::CLVector<BooleanClause*> allClauses;
    int32_t i = 0;
    while ( queries[i] != NULL ){
		BooleanQuery* bq = (BooleanQuery*)queries[i];
		BooleanClause** clauses = bq->getClauses();
		int32_t j = 0;
		while ( clauses[j] != NULL ){
			allClauses.push_back(clauses[j]);
			j++;
		}
		_CLDELETE_ARRAY(clauses);
		i++;
    }

    BooleanQuery* result = _CLNEW BooleanQuery();
    CL_NS(util)::CLVector<BooleanClause*>::iterator itr = allClauses.begin();
    while (itr != allClauses.end() ) {
		result->add(*itr);
    }
    return result;
}

Query::Query(const Query& clone):boost(clone.boost){
		//constructor
}
Weight* Query::createWeight(Searcher* searcher){
	_CLTHROWA(CL_ERR_UnsupportedOperation,"UnsupportedOperationException");
	return NULL;
}

Query::Query():
   boost(1.0f)
{
	//constructor
}
Query::~Query(){
}

/** Expert: called to re-write queries into primitive queries. */
Query* Query::rewrite(CL_NS(index)::IndexReader* reader){
   return this;
}

Query* Query::combine(Query** queries){
   _CLTHROWA(CL_ERR_UnsupportedOperation,"UnsupportedOperationException");
   return NULL;
}
Similarity* Query::getSimilarity(Searcher* searcher) {
   return searcher->getSimilarity();
}
bool Query::instanceOf(const TCHAR* other) const{
   const TCHAR* t = getQueryName();
	if ( t==other || _tcscmp( t, other )==0 )
		return true;
	else
		return false;
}
TCHAR* Query::toString() const{
   return toString(LUCENE_BLANK_STRING);
}

void Query::setBoost(float_t b) { boost = b; }

float_t Query::getBoost() const { return boost; }

Weight* Query::weight(Searcher* searcher){
    Query* query = searcher->rewrite(this);
    Weight* weight = query->createWeight(searcher);
    float_t sum = weight->sumOfSquaredWeights();
    float_t norm = getSimilarity(searcher)->queryNorm(sum);
    weight->normalize(norm);
    return weight;
}

TopFieldDocs::~TopFieldDocs(){
	if ( fields != NULL ){
		int i=0;
		while ( fields[i]!=NULL ){
			_CLDELETE(fields[i]);
			i++;
		}
		_CLDELETE_ARRAY(fields);
	}
}


TopDocs::TopDocs(const int32_t th, ScoreDoc **sds, int32_t scoreDocsLen):
    totalHits(th),
	scoreDocsLength(scoreDocsLen){
//Func - Constructor
//Pre  - sds may or may not be NULL
//       sdLength >= 0
//Post - The instance has been created

	scoreDocs       = sds;
	deleteScoreDocs = true;

}

TopDocs::~TopDocs(){
//Func - Destructor
//Pre  - true
//Post - The instance has been destroyed

	//Check if scoreDocs is valid
	if (scoreDocs){
        if ( deleteScoreDocs ){
			for ( int32_t i=0;scoreDocs[i]!=NULL;i++ ){
				_CLDELETE(scoreDocs[i]);
			}
		}
        _CLDELETE_ARRAY(scoreDocs);
	}
}

CL_NS_END
