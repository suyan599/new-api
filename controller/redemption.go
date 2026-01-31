package controller

import (
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 全局随机数生成器，线程安全
var (
	rng    *rand.Rand
	rngMux sync.Mutex
)

func init() {
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	type RedemptionRequest struct {
		Name        string `json:"name"`
		Count       int    `json:"count"`
		Quota       int    `json:"quota"`
		ExpiredTime int64  `json:"expired_time"`
		RandomMode  bool   `json:"random_mode"`
		MinQuota    int    `json:"min_quota"`
		MaxQuota    int    `json:"max_quota"`
	}

	var reqData RedemptionRequest
	if err := c.ShouldBindJSON(&reqData); err != nil {
		common.ApiError(c, err)
		return
	}

	if utf8.RuneCountInString(reqData.Name) == 0 || utf8.RuneCountInString(reqData.Name) > 20 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "兑换码名称长度必须在1-20之间",
		})
		return
	}
	if reqData.Count <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "兑换码个数必须大于0",
		})
		return
	}
	if reqData.Count > 100 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "一次兑换码批量生成的个数不能大于 100",
		})
		return
	}

	// 验证随机模式参数
	if reqData.RandomMode {
		if reqData.MinQuota <= 0 || reqData.MaxQuota <= 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "随机模式下最小额度和最大额度必须大于0",
			})
			return
		}
		if reqData.MinQuota >= reqData.MaxQuota {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "最小额度必须小于最大额度",
			})
			return
		}
	} else {
		if reqData.Quota <= 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "固定模式下额度必须大于0",
			})
			return
		}
	}

	if err := validateExpiredTime(reqData.ExpiredTime); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 批量生成兑换码数据
	var redemptions []model.Redemption
	var keys []string
	userId := c.GetInt("id")
	createdTime := common.GetTimestamp()

	for i := 0; i < reqData.Count; i++ {
		key := common.GetUUID()
		quota := reqData.Quota

		// 随机模式生成随机额度（线程安全）
		if reqData.RandomMode {
			rngMux.Lock()
			quota = rng.Intn(reqData.MaxQuota-reqData.MinQuota+1) + reqData.MinQuota
			rngMux.Unlock()
		}

		redemptions = append(redemptions, model.Redemption{
			UserId:      userId,
			Name:        reqData.Name,
			Key:         key,
			CreatedTime: createdTime,
			Quota:       quota,
			ExpiredTime: reqData.ExpiredTime,
		})
		keys = append(keys, key)
	}

	// 批量插入数据库
	if err := model.DB.CreateInBatches(redemptions, 50).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if statusOnly == "" {
		if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return errors.New("过期时间不能早于当前时间")
	}
	return nil
}
