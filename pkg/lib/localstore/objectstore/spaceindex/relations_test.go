package spaceindex

import (
	context2 "context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/anytype-heart/core/domain"
	"github.com/anyproto/anytype-heart/core/relationutils"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
)

// TODO Decide what to do with it
// func TestGetAggregatedOptions(t *testing.T) {
// 	t.Run("with no options", func(t *testing.T) {
// 		s := newFixture(t)
//
// 		got, err := s.GetAggregatedOptions(bundle.RelationKeyTag.String())
// 		require.NoError(t, err)
// 		assert.Empty(t, got)
// 	})
//
// 	t.Run("with options", func(t *testing.T) {
// 		s := newFixture(t)
// 		opt1 := makeRelationOptionObject("id1", "name1", "color1", bundle.RelationKeyTag.String())
// 		opt2 := makeRelationOptionObject("id2", "name2", "color2", bundle.RelationKeyStatus.String())
// 		opt3 := makeRelationOptionObject("id3", "name3", "color3", bundle.RelationKeyTag.String())
// 		s.AddObjects(t, []objectstore.TestObject{opt1, opt2, opt3})
//
// 		got, err := s.GetAggregatedOptions(bundle.RelationKeyTag.String())
// 		require.NoError(t, err)
// 		want := []*model.RelationOption{
// 			{
// 				Id:          "id1",
// 				Text:        "name1",
// 				Color:       "color1",
// 				RelationKey: bundle.RelationKeyTag.String(),
// 			},
// 			{
// 				Id:          "id3",
// 				Text:        "name3",
// 				Color:       "color3",
// 				RelationKey: bundle.RelationKeyTag.String(),
// 			},
// 		}
// 		assert.Equal(t, want, got)
// 	})
// }

func TestGetRelationById(t *testing.T) {
	t.Run("relation is not found", func(t *testing.T) {
		s := NewStoreFixture(t)

		_, err := s.GetRelationById("relationID")
		require.Error(t, err)
	})

	t.Run("requested object is not relation", func(t *testing.T) {
		s := NewStoreFixture(t)

		obj := TestObject{
			bundle.RelationKeyId:      domain.String("id1"),
			bundle.RelationKeyName:    domain.String("name1"),
			bundle.RelationKeySpaceId: domain.String("space1"),
		}
		s.AddObjects(t, []TestObject{obj})

		_, err := s.GetRelationById("id1")
		require.Error(t, err)
	})

	t.Run("relation is found", func(t *testing.T) {
		s := NewStoreFixture(t)

		relation := &relationutils.Relation{Relation: bundle.MustGetRelation(bundle.RelationKeyName)}
		relationID := "derivedFrom(name)"
		relation.Id = relationID
		relObject := relation.ToDetails()
		err := s.UpdateObjectDetails(context2.Background(), relation.Id, relObject)
		require.NoError(t, err)

		got, err := s.GetRelationById(relationID)
		require.NoError(t, err)
		assert.Equal(t, relationutils.RelationFromDetails(relObject).Relation, got)
	})
}
