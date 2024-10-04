package oracle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ types.ContractConfigTracker = (*contractConfigTracker)(nil)

type contractConfigTracker struct {
	logger            logger.Logger
	config            types.ContractConfig
	latestBlockHeight uint64
}

func NewContractConfigTracker(logger logger.Logger, oracleIdentity Identity) (*contractConfigTracker, error) {
	// THIS IS A HACK
	configJSON := `{"ConfigCount":29,"Signers":["azhPPNfXvCmNIPFoUyzdt7EW/R4=","0BLIw9QHdgADEST8EoihMae7Apg=","vCGG2iuOZ5v1ABVXrXiDoh4fheU=","QbUgrs/L4eT7/H9nkGI0NbKDYrg="],"Transmitters":["0x1D8E22c497F1BD188Cf57c14095c378B1f4763BD","0x81f8229eaBAa88023CEB818c3f86Cdfc1Af1F8A8","0xD38dB00fe9ae977D28192FF302f69c2975cE126E","0x427AA264121BdaB256e314cbC1aBc06561eD1c49"],"F":1,"OnchainConfig":"","OffchainConfigVersion":30,"OffchainConfig":"yAGA5JfQEtABgOSX0BLYAYCo1rkH4AGAyrXuAegBgNiO4W/wAQr6AQQCAwQFggIgYfBrtZOaI/0Aawz/GJ4ht8BBRB7chftRbtf+o1l8wAmCAiBZ9Hp1flxUB3a0dRBuzCH7LE306gYCcn60XOMghW9sDYICILLxsanWNGwreM0z0IVIBEtmIhdWy8nLSxgSV+/XYi+YggIgdTOWjP4OqMV9GYUnn1btk421+Y9LcJ9qjzzJUSCYmHOKAjQxMkQzS29vV0R6ZUgydWJZQWlUMnN5a1U0MjI4WWo2TEc0ZzJnRFBZR3ljYmo0b0txWDdZigI0MTJEM0tvb1dGMlFFSmJNZ0R3VDRDbXhGdW9BYlgxUEFBYmVmNFRwVTVtYlN5enFBZ3lDQYoCNDEyRDNLb29XUUhoMUxZOXFvb2I5N1I4NWtuRzNXMjR5TlB2RFdDR2pTeG9Mb3FXZE5VSnmKAjQxMkQzS29vV0pweTFuVllYbVd6anVCclBzcVRGcm1zU0VVZFFnZWp1NGJGdjV4Nmd3Tmh3mAKAlOvcA6ACgJTr3AOoAoCU69wDsAKAlOvcA7oCjAEKIIonz72hRGemuxPxy1ugKhxbdzlpn0c3KyRvk9nMyD9SEiBQHlHh5AgoD60EZYBZdNPHI7pMZfV+mz/2JyFBcCv5sBoQivYwoRU2BzLbhEvSWihLPxoQG7Hg2yhKTD8dj+lHKzUstRoQMX/mg/glcoxS+DSIBtaY3hoQfzVrWGo0/tRF6I8WVkcYYcACgOSX0BLIAoCU69wD"}`
	latestBlockHeight := uint64(12750)

	var config types.ContractConfig

	err := json.Unmarshal([]byte(configJSON), &config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract config: %v", err)
	}

	configDigestBytes, err := hex.DecodeString("000192171524191d04b8e50ce8b2019cc4297c595dfa1e2420fb161193e49e2e")
	if err != nil {
		return nil, fmt.Errorf("failed to decode config digest: %v", err)
	}

	configDigest, err := types.BytesToConfigDigest(configDigestBytes)

	if err != nil {
		return nil, fmt.Errorf("failed to convert bytes to ConfigDigest: %v", err)
	}
	config.ConfigDigest = configDigest

	return &contractConfigTracker{
		logger:            logger,
		config:            config,
		latestBlockHeight: latestBlockHeight,
	}, nil
}

func (cct *contractConfigTracker) Notify() <-chan struct{} {
	return nil
}

// TODO: Implement the LatestConfigDetails method
func (cct *contractConfigTracker) LatestConfigDetails(ctx context.Context) (
	changedInBlock uint64,
	configDigest types.ConfigDigest,
	err error,
) {
	cct.logger.Debugf("CCT: Returning latest config details: %s", cct.config.ConfigDigest)
	return 11414, cct.config.ConfigDigest, err
}

// TODO: Implement the LatestConfig method
func (cct *contractConfigTracker) LatestConfig(ctx context.Context, changedInBlock uint64) (types.ContractConfig, error) {
	cct.logger.Debugf("CCT: LatestConfig: %s", cct.config)
	return cct.config, nil
}

// TODO: Implement the LatestBlockHeight method
func (cct *contractConfigTracker) LatestBlockHeight(ctx context.Context) (blockHeight uint64, err error) {
	cct.logger.Debugf("CCT: LatestBlockHeight: %s", cct.latestBlockHeight)
	return cct.latestBlockHeight, nil
}
